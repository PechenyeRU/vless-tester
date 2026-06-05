// This file is the admin/read surface of the coordinator API: the endpoints the
// SvelteKit dashboard calls to inspect servers, the fleet, and aggregate stats,
// and to manage sources, settings, and trigger out-of-band actions (DESIGN 6).
// It is a separate trust domain from the worker control plane in api.go: workers
// authenticate with WORKER_TOKEN, the admin UI with a distinct admin token, so a
// compromised worker can never reach these mutating endpoints.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

// AdminStore is the data layer the admin API needs. The real *store.Store
// satisfies it; tests inject a fake.
type AdminStore interface {
	ListServers(ctx context.Context, f store.ServerFilter) ([]store.ServerSummary, error)
	GetServer(ctx context.Context, id int64) (model.Server, error)
	ServerHistory(ctx context.Context, serverID int64, limit int) ([]store.RunRecord, error)
	ListWorkers(ctx context.Context) ([]model.Worker, error)
	Stats(ctx context.Context) (store.Stats, error)
	ListAllSources(ctx context.Context) ([]model.Source, error)
	UpsertSource(ctx context.Context, kind model.SourceKind, location string) error
	SetSourceEnabled(ctx context.Context, id int64, enabled bool) error
	AllSettings(ctx context.Context) (map[string]json.RawMessage, error)
	SetSetting(ctx context.Context, key string, value any) error
}

// actions are the manual triggers the admin UI can fire. The coordinator maps
// each to a scheduler job; tests inject a recorder.
var adminActions = map[string]bool{
	"refresh-sources": true,
	"retest":          true,
	"publish":         true,
	"refresh-geoip":   true,
}

// AdminServer serves the admin/read API.
type AdminServer struct {
	Store AdminStore
	// Token is the admin bearer secret. When empty, auth is disabled (dev only).
	Token string
	// Action triggers an out-of-band coordinator job by name (one of adminActions).
	// nil disables the action endpoints (they return 503).
	Action func(name string) error
	// Logf is an optional logger; nil discards.
	Logf func(format string, args ...any)
}

func (s *AdminServer) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Handler builds the routed, authenticated http.Handler for the admin API.
func (s *AdminServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/servers", s.handleServers)
	mux.HandleFunc("GET /api/v1/servers/{id}", s.handleServerDetail)
	mux.HandleFunc("GET /api/v1/workers", s.handleWorkers)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/sources", s.handleListSources)
	mux.HandleFunc("PUT /api/v1/sources", s.handlePutSource)
	mux.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/v1/actions/{name}", s.handleAction)
	if s.Token == "" {
		s.logf("api: WARNING no admin token configured, admin plane is unauthenticated")
	}
	return s.withAuth(mux)
}

func (s *AdminServer) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- GET /servers ---

func (s *AdminServer) handleServers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ServerFilter{
		Country: q.Get("country"),
		Worker:  q.Get("worker"),
	}
	if v := q.Get("min_speed"); v != "" {
		f.MinSpeed, _ = strconv.ParseFloat(v, 64)
	}
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	servers, err := s.Store.ListServers(r.Context(), f)
	if err != nil {
		s.logf("api: list servers: %v", err)
		writeErr(w, http.StatusInternalServerError, "list servers failed")
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

// --- GET /servers/{id} ---

type serverDetailResp struct {
	Server  serverView        `json:"server"`
	History []store.RunRecord `json:"history"`
}

// serverView exposes a server's public + diagnostic fields for the admin detail
// view. RawURI is included here (admin-only), never in the public output.
type serverView struct {
	ID       int64          `json:"id"`
	Protocol model.Protocol `json:"protocol"`
	Host     string         `json:"host"`
	Port     int            `json:"port"`
	Country  string         `json:"country"`
	SeqName  string         `json:"seq_name"`
	RawURI   string         `json:"raw_uri"`
}

func (s *AdminServer) handleServerDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	srv, err := s.Store.GetServer(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "server not found")
		return
	}
	history, err := s.Store.ServerHistory(r.Context(), id, 0)
	if err != nil {
		s.logf("api: server history %d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "history failed")
		return
	}
	writeJSON(w, http.StatusOK, serverDetailResp{
		Server: serverView{
			ID: srv.ID, Protocol: srv.Protocol, Host: srv.Host, Port: srv.Port,
			Country: srv.Country, SeqName: srv.SeqName, RawURI: srv.RawURI,
		},
		History: history,
	})
}

// --- GET /workers ---

func (s *AdminServer) handleWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.Store.ListWorkers(r.Context())
	if err != nil {
		s.logf("api: list workers: %v", err)
		writeErr(w, http.StatusInternalServerError, "list workers failed")
		return
	}
	writeJSON(w, http.StatusOK, workers)
}

// --- GET /stats ---

func (s *AdminServer) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats(r.Context())
	if err != nil {
		s.logf("api: stats: %v", err)
		writeErr(w, http.StatusInternalServerError, "stats failed")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// --- GET/PUT /sources ---

func (s *AdminServer) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.Store.ListAllSources(r.Context())
	if err != nil {
		s.logf("api: list sources: %v", err)
		writeErr(w, http.StatusInternalServerError, "list sources failed")
		return
	}
	writeJSON(w, http.StatusOK, sources)
}

type putSourceReq struct {
	Kind     model.SourceKind `json:"kind"`
	Location string           `json:"location"`
	// Enabled, when present, toggles an existing source instead of upserting.
	Enabled *bool  `json:"enabled,omitempty"`
	ID      *int64 `json:"id,omitempty"`
}

func (s *AdminServer) handlePutSource(w http.ResponseWriter, r *http.Request) {
	var req putSourceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Toggle path: {id, enabled} flips an existing source.
	if req.ID != nil && req.Enabled != nil {
		if err := s.Store.SetSourceEnabled(r.Context(), *req.ID, *req.Enabled); err != nil {
			s.logf("api: set source enabled: %v", err)
			writeErr(w, http.StatusInternalServerError, "update source failed")
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	// Upsert path: {kind, location}.
	if req.Location == "" {
		writeErr(w, http.StatusBadRequest, "location is required")
		return
	}
	if req.Kind != model.SourceRawFile && req.Kind != model.SourceSubscriptionURL {
		writeErr(w, http.StatusBadRequest, "invalid kind")
		return
	}
	if err := s.Store.UpsertSource(r.Context(), req.Kind, req.Location); err != nil {
		s.logf("api: upsert source: %v", err)
		writeErr(w, http.StatusInternalServerError, "upsert source failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- GET/PUT /settings ---

func (s *AdminServer) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.Store.AllSettings(r.Context())
	if err != nil {
		s.logf("api: settings: %v", err)
		writeErr(w, http.StatusInternalServerError, "settings failed")
		return
	}
	writeJSON(w, http.StatusOK, all)
}

func (s *AdminServer) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	// The body is a partial map of key -> JSON value; each entry is upserted.
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(patch) == 0 {
		writeErr(w, http.StatusBadRequest, "no settings provided")
		return
	}
	for k, v := range patch {
		if err := s.Store.SetSetting(r.Context(), k, v); err != nil {
			s.logf("api: set setting %s: %v", k, err)
			writeErr(w, http.StatusInternalServerError, "set setting failed")
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// --- POST /actions/{name} ---

func (s *AdminServer) handleAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !adminActions[name] {
		writeErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if s.Action == nil {
		writeErr(w, http.StatusServiceUnavailable, "actions unavailable")
		return
	}
	if err := s.Action(name); err != nil {
		s.logf("api: action %s: %v", name, err)
		writeErr(w, http.StatusInternalServerError, "action failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"triggered": name})
}
