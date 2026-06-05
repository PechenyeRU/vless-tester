// Package api is the coordinator's worker control plane: a small REST/JSON
// surface that lets untrusted, pull-based workers register, heartbeat, claim
// jobs, report results, and release work. Each worker authenticates with its own
// token, minted in the admin panel; the token's name is the worker's identity
// (DESIGN 6 and 11). The handlers are deliberately thin: every trust decision
// (job ownership, plausibility bounds, fan-out) lives in the store/engine, so a
// malicious worker cannot reach past this layer.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

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
	// MediaChecks returns the media-unlock platforms to probe, or nil when media
	// checks are disabled.
	MediaChecks(ctx context.Context) ([]string, error)
}

// WorkerTokenResolver maps a presented bearer secret to a worker identity. The
// real *store.Store satisfies it; tests inject a fake.
type WorkerTokenResolver interface {
	ResolveWorkerToken(ctx context.Context, token string) (name string, ok bool, err error)
}

// Server serves the worker control plane.
type Server struct {
	Store Store
	// Tokens resolves per-worker tokens minted from the admin panel: a matching
	// bearer authenticates the request AS that worker, and the token's name is the
	// worker's identity (it overrides any client-sent id). When nil, auth is
	// disabled (local development only) and Handler logs a warning.
	Tokens WorkerTokenResolver
	// Bounds caps implausible worker-reported numbers; the zero value uses
	// sensible defaults.
	Bounds Bounds
	// Logf is an optional logger; nil discards.
	Logf func(format string, args ...any)
}

// workerIdentityKey carries the authenticated worker name (from a per-worker
// token) through the request context.
type ctxKey int

const workerIdentityKey ctxKey = iota

func withIdentity(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, workerIdentityKey, name)
}

func identityFrom(ctx context.Context) string {
	v, _ := ctx.Value(workerIdentityKey).(string)
	return v
}

// authWorkerID returns the token-authenticated identity when present, otherwise
// the client-supplied fallback (legacy shared-token path).
func authWorkerID(r *http.Request, fallback string) string {
	if id := identityFrom(r.Context()); id != "" {
		return id
	}
	return fallback
}

func bearerToken(r *http.Request) string {
	const p = "Bearer "
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, p) {
		return h[len(p):]
	}
	return ""
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
	if s.Tokens == nil {
		s.logf("api: WARNING no worker tokens configured, control plane is unauthenticated")
	}
	return s.withAuth(mux)
}

// withAuth authenticates each request against the per-worker tokens, resolving
// the bearer to a worker identity carried in the context. With no resolver
// configured, requests pass through (local development only).
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Tokens == nil {
			next.ServeHTTP(w, r) // dev mode: no auth configured
			return
		}
		bearer := bearerToken(r)
		if bearer != "" {
			name, ok, err := s.Tokens.ResolveWorkerToken(r.Context(), bearer)
			if err != nil {
				s.logf("api: resolve worker token: %v", err)
				writeErr(w, http.StatusInternalServerError, "auth error")
				return
			}
			if ok {
				next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), name)))
				return
			}
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized")
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
	// A per-worker token fixes the identity from the panel; otherwise fall back
	// to the client-supplied id, generating a mnemonic when blank.
	id := authWorkerID(r, req.ID)
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
	id := authWorkerID(r, req.ID)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	status := req.Status
	if status == "" {
		status = "idle"
	}
	if err := s.Store.Heartbeat(r.Context(), id, status); err != nil {
		s.logf("api: heartbeat %s: %v", id, err)
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
	JobID    int64    `json:"job_id"`
	ServerID int64    `json:"server_id"`
	RawURI   string   `json:"raw_uri"`
	Phase    string   `json:"phase"`
	Protocol string   `json:"protocol"`
	Checks   []string `json:"checks,omitempty"`
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req claimReq
	if !decode(w, r, &req) {
		return
	}
	workerID := authWorkerID(r, req.WorkerID)
	if workerID == "" {
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
	jobs, err := s.Store.ClaimJobs(r.Context(), workerID, model.JobPhase(req.Phase), n)
	if err != nil {
		s.logf("api: claim %s: %v", workerID, err)
		writeErr(w, http.StatusInternalServerError, "claim failed")
		return
	}
	// The platform list is policy the coordinator owns; it rides along with every
	// claimed job so the worker knows what to probe.
	platforms, err := s.Store.MediaChecks(r.Context())
	if err != nil {
		s.logf("api: claim %s: media settings: %v", workerID, err)
		platforms = nil // media checks are best-effort; never fail a claim over them
	}
	out := make([]claimedJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, claimedJob{
			JobID:    j.JobID,
			ServerID: j.ServerID,
			RawURI:   j.RawURI,
			Phase:    string(j.Phase),
			Protocol: string(j.Protocol),
			Checks:   platforms,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- jobs/results ---

type resultItem struct {
	JobID     int64                `json:"job_id"`
	Status    string               `json:"status"`
	LatencyMs *int                 `json:"latency_ms,omitempty"`
	DlMbps    *float64             `json:"dl_mbps,omitempty"`
	UlMbps    *float64             `json:"ul_mbps,omitempty"`
	Error     string               `json:"error,omitempty"`
	Checks    []model.CheckOutcome `json:"checks,omitempty"`
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
	workerID := authWorkerID(r, req.WorkerID)
	if workerID == "" {
		writeErr(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	accepted := 0
	for _, item := range req.Results {
		// The worker is untrusted: bound its numbers before they reach history.
		run := s.Bounds.sanitize(item)
		ok, err := s.Store.RecordResult(r.Context(), workerID, item.JobID, run)
		if err != nil {
			s.logf("api: results %s job %d: %v", workerID, item.JobID, err)
			writeErr(w, http.StatusInternalServerError, "results failed")
			return
		}
		if ok {
			accepted++
		} else {
			// Job missing or not held by this worker: a stale or forged report.
			s.logf("api: results %s dropped job %d (not owned)", workerID, item.JobID)
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
	workerID := authWorkerID(r, req.WorkerID)
	if workerID == "" {
		writeErr(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	if _, err := s.Store.NackJobs(r.Context(), workerID, req.JobIDs); err != nil {
		s.logf("api: nack %s: %v", workerID, err)
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
