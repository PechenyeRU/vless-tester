// This file is the admin/read surface of the coordinator API: the endpoints the
// SvelteKit dashboard calls to inspect servers, the fleet, and aggregate stats,
// and to manage sources, settings, and trigger out-of-band actions (DESIGN 6).
// It is a separate trust domain from the worker control plane in api.go: workers
// authenticate with their own per-worker tokens, the admin UI with a distinct
// admin token, so a compromised worker can never reach these mutating endpoints.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

// AdminStore is the data layer the admin API needs. The real *store.Store
// satisfies it; tests inject a fake.
type AdminStore interface {
	ListServers(ctx context.Context, f store.ServerFilter) ([]store.ServerSummary, error)
	GetServer(ctx context.Context, id int64) (model.Server, error)
	ServerHistory(ctx context.Context, serverID int64, limit int) ([]store.RunRecord, error)
	ServerChecks(ctx context.Context, serverID int64) ([]model.CheckOutcome, error)
	ListWorkers(ctx context.Context) ([]model.Worker, error)
	Stats(ctx context.Context) (store.Stats, error)
	ListAllSources(ctx context.Context) ([]model.Source, error)
	UpsertSource(ctx context.Context, kind model.SourceKind, location string) error
	SetSourceEnabled(ctx context.Context, id int64, enabled bool) error
	AllSettings(ctx context.Context) (map[string]json.RawMessage, error)
	SetSetting(ctx context.Context, key string, value any) error
	CreateWorkerToken(ctx context.Context, name string, protocols []string) (string, error)
	ListWorkerTokens(ctx context.Context) ([]model.WorkerToken, error)
	DeleteWorkerToken(ctx context.Context, id int64) (bool, error)
	SetWorkerTokenProtocols(ctx context.Context, id int64, protocols []string) (bool, error)
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
	// Username and Password gate the human-facing /login endpoint, which exchanges
	// them for Token. When Password is empty it falls back to Token, so a
	// deployment that only sets ADMIN_TOKEN can still sign in (password = token).
	Username string
	Password string
	// Action triggers an out-of-band coordinator job by name (one of adminActions).
	// nil disables the action endpoints (they return 503).
	Action func(name string) error
	// Logf is an optional logger; nil discards.
	Logf func(format string, args ...any)
}

// loginPassword is the secret /login accepts, falling back to the bearer token
// when no explicit password is configured.
func (s *AdminServer) loginPassword() string {
	if s.Password != "" {
		return s.Password
	}
	return s.Token
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
	mux.HandleFunc("GET /api/v1/worker-tokens", s.handleListWorkerTokens)
	mux.HandleFunc("POST /api/v1/worker-tokens", s.handleCreateWorkerToken)
	mux.HandleFunc("PUT /api/v1/worker-tokens/{id}", s.handleSetWorkerTokenProtocols)
	mux.HandleFunc("DELETE /api/v1/worker-tokens/{id}", s.handleDeleteWorkerToken)
	mux.HandleFunc("POST /api/v1/login", s.handleLogin)
	if s.Token == "" {
		s.logf("api: WARNING no admin token configured, admin plane is unauthenticated")
	}
	return s.withAuth(mux)
}

func (s *AdminServer) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /login is the unauthenticated entry point: it validates credentials
		// itself and hands back the bearer token used for every other call.
		if r.URL.Path == "/api/v1/login" {
			next.ServeHTTP(w, r)
			return
		}
		if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- /worker-tokens (per-worker control-plane credentials) ---

type workerTokenView struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used"`
	Protocols []string   `json:"protocols"`
}

func (s *AdminServer) handleListWorkerTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Store.ListWorkerTokens(r.Context())
	if err != nil {
		s.logf("api: list worker tokens: %v", err)
		writeErr(w, http.StatusInternalServerError, "list tokens failed")
		return
	}
	views := make([]workerTokenView, len(tokens))
	for i, t := range tokens {
		views[i] = workerTokenView{
			ID: t.ID, Name: t.Name, Enabled: t.Enabled,
			CreatedAt: t.CreatedAt, LastUsed: t.LastUsed, Protocols: t.Protocols,
		}
	}
	writeJSON(w, http.StatusOK, views)
}

type createTokenReq struct {
	Name string `json:"name"`
	// Protocols is the optional per-worker allow-list (empty = all protocols).
	Protocols []string `json:"protocols"`
}

// handleCreateWorkerToken mints a token for a worker name and returns the secret
// once. The plaintext is never retrievable again.
func (s *AdminServer) handleCreateWorkerToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	token, err := s.Store.CreateWorkerToken(r.Context(), req.Name, req.Protocols)
	if errors.Is(err, store.ErrWorkerNameTaken) {
		writeErr(w, http.StatusConflict, "a token for that worker name already exists")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "token": token})
}

type setProtocolsReq struct {
	Protocols []string `json:"protocols"`
}

// handleSetWorkerTokenProtocols replaces a worker's protocol allow-list (empty =
// all protocols).
func (s *AdminServer) handleSetWorkerTokenProtocols(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req setProtocolsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ok, err := s.Store.SetWorkerTokenProtocols(r.Context(), id, req.Protocols)
	if err != nil {
		s.logf("api: set worker token protocols %d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "update token failed")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleDeleteWorkerToken revokes a token by id.
func (s *AdminServer) handleDeleteWorkerToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleted, err := s.Store.DeleteWorkerToken(r.Context(), id)
	if err != nil {
		s.logf("api: delete worker token %d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "delete token failed")
		return
	}
	if !deleted {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /login ---

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin exchanges a username/password for the admin bearer token, so the
// dashboard can offer a human login instead of asking users to paste a token.
func (s *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.Token == "" {
		writeErr(w, http.StatusServiceUnavailable, "login unavailable: no admin credentials configured")
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.loginPassword())) == 1
	if !userOK || !passOK {
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": s.Token})
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
	Server  serverView           `json:"server"`
	History []store.RunRecord    `json:"history"`
	Checks  []model.CheckOutcome `json:"checks"`
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
	checks, err := s.Store.ServerChecks(r.Context(), id)
	if err != nil {
		s.logf("api: server checks %d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "checks failed")
		return
	}
	writeJSON(w, http.StatusOK, serverDetailResp{
		Server: serverView{
			ID: srv.ID, Protocol: srv.Protocol, Host: srv.Host, Port: srv.Port,
			Country: srv.Country, SeqName: srv.SeqName, RawURI: srv.RawURI,
		},
		History: history,
		Checks:  checks,
	})
}

// --- GET /workers ---

// workerView serializes a worker with lowercase keys, consistent with the rest
// of the admin API (model.Worker itself is untagged and used on the wire).
type workerView struct {
	ID       string         `json:"id"`
	Capacity model.Capacity `json:"capacity"`
	Status   string         `json:"status"`
	LastSeen time.Time      `json:"last_seen"`
}

func (s *AdminServer) handleWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.Store.ListWorkers(r.Context())
	if err != nil {
		s.logf("api: list workers: %v", err)
		writeErr(w, http.StatusInternalServerError, "list workers failed")
		return
	}
	views := make([]workerView, len(workers))
	for i, wk := range workers {
		views[i] = workerView{ID: wk.ID, Capacity: wk.Capacity, Status: wk.Status, LastSeen: wk.LastSeen}
	}
	writeJSON(w, http.StatusOK, views)
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

// sourceView serializes a source with lowercase keys for the dashboard
// (model.Source is untagged and used internally).
type sourceView struct {
	ID        int64            `json:"id"`
	Kind      model.SourceKind `json:"kind"`
	Location  string           `json:"location"`
	LastFetch *time.Time       `json:"last_fetch"`
	Enabled   bool             `json:"enabled"`
}

func (s *AdminServer) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.Store.ListAllSources(r.Context())
	if err != nil {
		s.logf("api: list sources: %v", err)
		writeErr(w, http.StatusInternalServerError, "list sources failed")
		return
	}
	views := make([]sourceView, len(sources))
	for i, src := range sources {
		views[i] = sourceView{
			ID: src.ID, Kind: src.Kind, Location: src.Location,
			LastFetch: src.LastFetch, Enabled: src.Enabled,
		}
	}
	writeJSON(w, http.StatusOK, views)
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
