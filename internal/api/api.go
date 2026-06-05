// Package api is the coordinator's worker control plane: a small REST/JSON
// surface that lets untrusted, pull-based workers register, heartbeat, claim
// jobs, report results, and release work. All endpoints sit behind a static
// bearer token (DESIGN 6 and 11). The handlers are deliberately thin: every
// trust decision (job ownership, plausibility bounds, fan-out) lives in the
// store/engine, so a malicious worker cannot reach past this layer.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/whitedns/vless-tester/internal/ident"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

// maxClaim caps how many jobs a single claim can lease, so one worker cannot
// drain the queue. defaultClaim applies when the worker asks for none.
const (
	defaultClaim = 32
	maxClaim     = 256
)

// Store is the subset of the data layer the control plane needs. The real
// *store.Store satisfies it; tests inject a fake so the handlers run without a
// database.
type Store interface {
	UpsertWorker(ctx context.Context, w model.Worker) error
	Heartbeat(ctx context.Context, workerID, status string) error
	ClaimJobs(ctx context.Context, workerID string, phase model.JobPhase, max int) ([]store.ClaimedJob, error)
	RecordResult(ctx context.Context, workerID string, jobID int64, r model.TestRun) (bool, error)
	NackJobs(ctx context.Context, workerID string, jobIDs []int64) (int64, error)
}

// Server serves the worker control plane.
type Server struct {
	Store Store
	// Token is the shared bearer secret. When empty, auth is disabled (intended
	// for local development only); Handler logs a warning in that case.
	Token string
	// Bounds caps implausible worker-reported numbers; the zero value uses
	// sensible defaults.
	Bounds Bounds
	// Logf is an optional logger; nil discards.
	Logf func(format string, args ...any)
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Handler builds the routed, authenticated http.Handler for the control plane.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workers/register", s.handleRegister)
	mux.HandleFunc("/api/v1/workers/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("/api/v1/jobs/claim", s.handleClaim)
	mux.HandleFunc("/api/v1/jobs/results", s.handleResults)
	mux.HandleFunc("/api/v1/jobs/nack", s.handleNack)
	if s.Token == "" {
		s.logf("api: WARNING no bearer token configured, control plane is unauthenticated")
	}
	return s.withAuth(mux)
}

// withAuth enforces the bearer token on every request. With no token configured
// it passes through (dev mode).
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- workers/register ---

type registerReq struct {
	ID       string         `json:"id,omitempty"`
	Capacity model.Capacity `json:"capacity"`
}

type registerResp struct {
	ID string `json:"id"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decode(w, r, &req) {
		return
	}
	id := req.ID
	if id == "" {
		id = ident.Mnemonic()
	}
	if err := s.Store.UpsertWorker(r.Context(), model.Worker{
		ID: id, Capacity: req.Capacity, Status: "idle",
	}); err != nil {
		s.logf("api: register %s: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "register failed")
		return
	}
	writeJSON(w, http.StatusOK, registerResp{ID: id})
}

// --- workers/heartbeat ---

type heartbeatReq struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	CapacityFree model.Capacity `json:"capacity_free"`
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatReq
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	status := req.Status
	if status == "" {
		status = "idle"
	}
	if err := s.Store.Heartbeat(r.Context(), req.ID, status); err != nil {
		s.logf("api: heartbeat %s: %v", req.ID, err)
		writeErr(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- jobs/claim ---

type claimReq struct {
	WorkerID string `json:"worker_id"`
	Phase    string `json:"phase,omitempty"`
	Max      int    `json:"max"`
}

type claimedJob struct {
	JobID    int64  `json:"job_id"`
	ServerID int64  `json:"server_id"`
	RawURI   string `json:"raw_uri"`
	Phase    string `json:"phase"`
	Protocol string `json:"protocol"`
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req claimReq
	if !decode(w, r, &req) {
		return
	}
	if req.WorkerID == "" {
		writeErr(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	if !validPhase(req.Phase) {
		writeErr(w, http.StatusBadRequest, "invalid phase")
		return
	}
	n := req.Max
	switch {
	case n <= 0:
		n = defaultClaim
	case n > maxClaim:
		n = maxClaim
	}
	jobs, err := s.Store.ClaimJobs(r.Context(), req.WorkerID, model.JobPhase(req.Phase), n)
	if err != nil {
		s.logf("api: claim %s: %v", req.WorkerID, err)
		writeErr(w, http.StatusInternalServerError, "claim failed")
		return
	}
	out := make([]claimedJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, claimedJob{
			JobID:    j.JobID,
			ServerID: j.ServerID,
			RawURI:   j.RawURI,
			Phase:    string(j.Phase),
			Protocol: string(j.Protocol),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- jobs/results ---

type resultItem struct {
	JobID     int64    `json:"job_id"`
	Status    string   `json:"status"`
	LatencyMs *int     `json:"latency_ms,omitempty"`
	DlMbps    *float64 `json:"dl_mbps,omitempty"`
	UlMbps    *float64 `json:"ul_mbps,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type resultsReq struct {
	WorkerID string       `json:"worker_id"`
	Results  []resultItem `json:"results"`
}

type resultsResp struct {
	Accepted int `json:"accepted"`
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	var req resultsReq
	if !decode(w, r, &req) {
		return
	}
	if req.WorkerID == "" {
		writeErr(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	accepted := 0
	for _, item := range req.Results {
		// The worker is untrusted: bound its numbers before they reach history.
		run := s.Bounds.sanitize(item)
		ok, err := s.Store.RecordResult(r.Context(), req.WorkerID, item.JobID, run)
		if err != nil {
			s.logf("api: results %s job %d: %v", req.WorkerID, item.JobID, err)
			writeErr(w, http.StatusInternalServerError, "results failed")
			return
		}
		if ok {
			accepted++
		} else {
			// Job missing or not held by this worker: a stale or forged report.
			s.logf("api: results %s dropped job %d (not owned)", req.WorkerID, item.JobID)
		}
	}
	writeJSON(w, http.StatusOK, resultsResp{Accepted: accepted})
}

// --- jobs/nack ---

type nackReq struct {
	WorkerID string  `json:"worker_id"`
	JobIDs   []int64 `json:"job_ids"`
}

func (s *Server) handleNack(w http.ResponseWriter, r *http.Request) {
	var req nackReq
	if !decode(w, r, &req) {
		return
	}
	if req.WorkerID == "" {
		writeErr(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	if _, err := s.Store.NackJobs(r.Context(), req.WorkerID, req.JobIDs); err != nil {
		s.logf("api: nack %s: %v", req.WorkerID, err)
		writeErr(w, http.StatusInternalServerError, "nack failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- helpers ---

func validPhase(p string) bool {
	switch model.JobPhase(p) {
	case "", model.PhaseLatency, model.PhaseSpeed, model.PhaseChecks:
		return true
	default:
		return false
	}
}

// normalizeStatus maps an untrusted worker-reported status onto a known value,
// defaulting unknown strings to "error".
func normalizeStatus(s string) model.RunStatus {
	switch model.RunStatus(s) {
	case model.StatusOK, model.StatusTimeout, model.StatusError, model.StatusRefused:
		return model.RunStatus(s)
	default:
		return model.StatusError
	}
}

// decode enforces POST + JSON body and writes the appropriate error. It returns
// false when the caller should stop.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
