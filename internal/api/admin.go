// This file is the admin/read surface of the coordinator API: the endpoints the
// SvelteKit dashboard calls to inspect servers, the fleet, and aggregate stats,
// and to manage sources, settings, and trigger out-of-band actions (DESIGN 6).
// It is a separate trust domain from the worker control plane in api.go: workers
// authenticate with their own per-worker tokens, the admin UI with a distinct
// admin token, so a compromised worker can never reach these mutating endpoints.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/whitedns/vless-tester/internal/logbuf"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/notify"
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
	CycleProgress(ctx context.Context) (store.CycleProgress, error)
	NotifySettings(ctx context.Context) (enabled bool, urls []string, err error)
}

// LogSource exposes recent log lines for the live-log endpoint. *logbuf.Hub
// satisfies it; a nil source disables the endpoint.
type LogSource interface {
	Since(seq int64) ([]logbuf.Entry, int64)
}

// actions are the manual triggers the admin UI can fire. The coordinator maps
// each to a scheduler job; tests inject a recorder.
var adminActions = map[string]bool{
	"refresh-sources": true,
	"retest":          true,
	"publish":         true,
	"refresh-geoip":   true,
}

// AdminServer serves the admin/read API. Authentication is session-based: a
// successful /login mints a random bearer token held in memory, which every
// other request must present. There is no static admin secret, and sessions do
// not survive a restart. When no password is configured, auth is disabled (dev
// only).
type AdminServer struct {
	Store AdminStore
	// Username and Password are the admin credentials /login checks. When Password
	// is empty, the admin plane is unauthenticated (dev only).
	Username string
	Password string
	// SessionTTL is how long a minted session stays valid; <=0 uses the default.
	SessionTTL time.Duration
	// Action triggers an out-of-band coordinator job by name (one of adminActions).
	// nil disables the action endpoints (they return 503).
	Action func(name string) error
	// Logs is the recent-log source for the live-log endpoint; nil disables it.
	Logs LogSource
	// Logf is an optional logger; nil discards.
	Logf func(format string, args ...any)

	mu       sync.Mutex
	sessions map[string]time.Time // token -> expiry
}

// defaultSessionTTL bounds how long a login stays valid.
const defaultSessionTTL = 12 * time.Hour

// authEnabled reports whether the admin plane requires authentication. Without a
// configured password there is no credential to mint sessions from, so the plane
// is left open (dev only).
func (s *AdminServer) authEnabled() bool { return s.Password != "" }

func (s *AdminServer) sessionTTL() time.Duration {
	if s.SessionTTL > 0 {
		return s.SessionTTL
	}
	return defaultSessionTTL
}

// newSession mints and stores a random bearer token, pruning expired ones.
func (s *AdminServer) newSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]time.Time)
	}
	now := time.Now()
	for t, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, t)
		}
	}
	s.sessions[token] = now.Add(s.sessionTTL())
	return token, nil
}

// validSession reports whether token is a live session, dropping it if expired.
func (s *AdminServer) validSession(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sessions, token)
		return false
	}
	return true
}

// revokeSession drops a session token (logout).
func (s *AdminServer) revokeSession(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
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
	mux.HandleFunc("GET /api/v1/progress", s.handleProgress)
	mux.HandleFunc("GET /api/v1/logs", s.handleLogs)
	mux.HandleFunc("POST /api/v1/notify-test", s.handleNotifyTest)
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
	mux.HandleFunc("POST /api/v1/logout", s.handleLogout)
	if !s.authEnabled() {
		s.logf("api: WARNING no admin password configured, admin plane is unauthenticated")
	}
	return s.withAuth(mux)
}

func (s *AdminServer) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /login is the unauthenticated entry point: it validates credentials
		// itself and hands back the session token used for every other call.
		if r.URL.Path == "/api/v1/login" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authEnabled() {
			next.ServeHTTP(w, r) // dev-open: no credentials configured
			return
		}
		bearer := bearerToken(r)
		if bearer == "" || !s.validSession(bearer) {
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

// handleLogin validates the admin credentials and mints a fresh session token
// the dashboard then attaches to every request. There is no static secret to
// hand out: each login gets its own in-memory token.
func (s *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		writeErr(w, http.StatusServiceUnavailable, "login unavailable: no admin credentials configured")
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.Password)) == 1
	if !userOK || !passOK {
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	token, err := s.newSession()
	if err != nil {
		s.logf("api: mint session: %v", err)
		writeErr(w, http.StatusInternalServerError, "could not start session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleLogout revokes the caller's session token. It is reached only through
// withAuth, so the bearer is a live session; revoking it makes it unusable
// immediately rather than waiting for the TTL.
func (s *AdminServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.revokeSession(bearerToken(r))
	w.WriteHeader(http.StatusNoContent)
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

// --- GET /progress ---

// progressView is the in-flight cycle progress with a derived percent and ETA.
type progressView struct {
	Active         bool    `json:"active"`
	BatchID        int64   `json:"batch_id,omitempty"`
	Total          int     `json:"total"`
	Completed      int     `json:"completed"`
	Open           int     `json:"open"`
	Done           int     `json:"done"`
	Failed         int     `json:"failed"`
	Percent        float64 `json:"percent"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	EtaSeconds     float64 `json:"eta_seconds"` // -1 when not yet estimable
	PerMinute      float64 `json:"per_minute"`
}

func (s *AdminServer) handleProgress(w http.ResponseWriter, r *http.Request) {
	cp, err := s.Store.CycleProgress(r.Context())
	if err != nil {
		s.logf("api: progress: %v", err)
		writeErr(w, http.StatusInternalServerError, "progress failed")
		return
	}
	v := progressView{
		Active: cp.Active, BatchID: cp.BatchID, Total: cp.Total,
		Done: cp.Done, Failed: cp.Failed, Open: cp.Open,
		Completed: cp.Done + cp.Failed, EtaSeconds: -1,
	}
	if cp.Active && cp.Total > 0 {
		v.Percent = float64(v.Completed) / float64(cp.Total) * 100
		elapsed := time.Since(cp.StartedAt).Seconds()
		v.ElapsedSeconds = elapsed
		if v.Completed > 0 && elapsed > 0 {
			rate := float64(v.Completed) / elapsed // jobs/sec
			v.PerMinute = rate * 60
			v.EtaSeconds = float64(v.Open) / rate
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// --- POST /notify-test ---

// handleNotifyTest sends a test notification to the currently configured
// shoutrrr URLs, so the operator can validate them without restarting the
// coordinator (which reads them at startup for the cycle notifier).
func (s *AdminServer) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	_, urls, err := s.Store.NotifySettings(r.Context())
	if err != nil {
		s.logf("api: notify-test settings: %v", err)
		writeErr(w, http.StatusInternalServerError, "read settings failed")
		return
	}
	n, err := notify.NewShoutrrr(urls)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if n == nil {
		writeErr(w, http.StatusBadRequest, "no notification URLs configured")
		return
	}
	if err := n.Notify(r.Context(), "🔔 vless-tester test notification"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- GET /logs ---

func (s *AdminServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		since, _ = strconv.ParseInt(v, 10, 64)
	}
	var lines []logbuf.Entry
	var next int64
	if s.Logs != nil {
		lines, next = s.Logs.Since(since)
	}
	if lines == nil {
		lines = []logbuf.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "next_seq": next})
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
