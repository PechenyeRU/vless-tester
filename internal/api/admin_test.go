package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/logbuf"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

var errNotFound = errors.New("not found")

// fakeAdminStore is an in-memory AdminStore so the admin handlers run with no
// database. Each field scripts one method's output; calls are recorded.
type fakeAdminStore struct {
	servers          []store.ServerSummary
	listFilter       store.ServerFilter
	server           model.Server
	getErr           error
	history          []store.RunRecord
	serverChecks     []model.CheckOutcome
	workers          []model.Worker
	stats            store.Stats
	progress         store.CycleProgress
	sources          []model.Source
	settings         map[string]json.RawMessage
	upserted         []model.Source
	toggled          map[int64]bool
	setSettings      map[string]json.RawMessage
	workerTokens     []model.WorkerToken
	createdToken     string
	createdProtocols []string
	deletedToken     int64
	setProtocolsID   int64
	setProtocols     []string
}

func newAdminFake() *fakeAdminStore {
	return &fakeAdminStore{
		settings:    map[string]json.RawMessage{},
		toggled:     map[int64]bool{},
		setSettings: map[string]json.RawMessage{},
	}
}

func (f *fakeAdminStore) ListServers(_ context.Context, fl store.ServerFilter) ([]store.ServerSummary, error) {
	f.listFilter = fl
	return f.servers, nil
}
func (f *fakeAdminStore) GetServer(_ context.Context, id int64) (model.Server, error) {
	if f.getErr != nil {
		return model.Server{}, f.getErr
	}
	f.server.ID = id
	return f.server, nil
}
func (f *fakeAdminStore) ServerHistory(_ context.Context, _ int64, _ int) ([]store.RunRecord, error) {
	return f.history, nil
}
func (f *fakeAdminStore) ServerChecks(_ context.Context, _ int64) ([]model.CheckOutcome, error) {
	return f.serverChecks, nil
}
func (f *fakeAdminStore) ListWorkers(_ context.Context) ([]model.Worker, error) {
	return f.workers, nil
}
func (f *fakeAdminStore) Stats(_ context.Context) (store.Stats, error) { return f.stats, nil }
func (f *fakeAdminStore) CycleProgress(_ context.Context) (store.CycleProgress, error) {
	return f.progress, nil
}
func (f *fakeAdminStore) ListAllSources(_ context.Context) ([]model.Source, error) {
	return f.sources, nil
}
func (f *fakeAdminStore) UpsertSource(_ context.Context, kind model.SourceKind, loc string) error {
	f.upserted = append(f.upserted, model.Source{Kind: kind, Location: loc})
	return nil
}
func (f *fakeAdminStore) SetSourceEnabled(_ context.Context, id int64, enabled bool) error {
	f.toggled[id] = enabled
	return nil
}
func (f *fakeAdminStore) AllSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return f.settings, nil
}
func (f *fakeAdminStore) SetSetting(_ context.Context, key string, value any) error {
	f.setSettings[key] = value.(json.RawMessage)
	return nil
}
func (f *fakeAdminStore) CreateWorkerToken(_ context.Context, name string, protocols []string) (string, error) {
	f.createdToken = name
	f.createdProtocols = protocols
	return "wt_fake-secret", nil
}
func (f *fakeAdminStore) ListWorkerTokens(_ context.Context) ([]model.WorkerToken, error) {
	return f.workerTokens, nil
}
func (f *fakeAdminStore) DeleteWorkerToken(_ context.Context, id int64) (bool, error) {
	f.deletedToken = id
	return true, nil
}
func (f *fakeAdminStore) SetWorkerTokenProtocols(_ context.Context, id int64, protocols []string) (bool, error) {
	f.setProtocolsID = id
	f.setProtocols = protocols
	return true, nil
}

// loginToken drives /login and returns the minted session token.
func loginToken(t *testing.T, s *AdminServer, user, pass string) string {
	t.Helper()
	rec := do(t, s, http.MethodPost, "/api/v1/login", "", `{"username":"`+user+`","password":"`+pass+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	var out struct{ Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if out.Token == "" {
		t.Fatal("login returned an empty token")
	}
	return out.Token
}

func TestAdminAuth(t *testing.T) {
	s := &AdminServer{Store: newAdminFake(), Username: "admin", Password: "hunter2"}

	if rec := do(t, s, http.MethodGet, "/api/v1/stats", "", ``); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodGet, "/api/v1/stats", "not-a-session", ``); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bogus token: want 401, got %d", rec.Code)
	}
	token := loginToken(t, s, "admin", "hunter2")
	if rec := do(t, s, http.MethodGet, "/api/v1/stats", token, ``); rec.Code != http.StatusOK {
		t.Fatalf("session token: want 200, got %d", rec.Code)
	}
}

func TestAdminAuthDisabledWithoutPassword(t *testing.T) {
	// No password configured: the plane is open (dev only) and /login is 503.
	s := &AdminServer{Store: newAdminFake(), Username: "admin"}
	if rec := do(t, s, http.MethodGet, "/api/v1/stats", "", ``); rec.Code != http.StatusOK {
		t.Fatalf("dev-open: want 200, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/login", "", `{"username":"admin","password":"x"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login without creds: want 503, got %d", rec.Code)
	}
}

func TestAdminLogin(t *testing.T) {
	s := &AdminServer{Store: newAdminFake(), Username: "admin", Password: "hunter2"}

	// Each login mints a distinct session token.
	t1 := loginToken(t, s, "admin", "hunter2")
	t2 := loginToken(t, s, "admin", "hunter2")
	if t1 == t2 {
		t.Fatal("expected distinct session tokens per login")
	}

	// Wrong password and wrong username are both rejected.
	if rec := do(t, s, http.MethodPost, "/api/v1/login", "", `{"username":"admin","password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: want 401, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/login", "", `{"username":"root","password":"hunter2"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad username: want 401, got %d", rec.Code)
	}
}

func TestAdminLogout(t *testing.T) {
	s := &AdminServer{Store: newAdminFake(), Username: "admin", Password: "hunter2"}
	token := loginToken(t, s, "admin", "hunter2")

	if rec := do(t, s, http.MethodGet, "/api/v1/stats", token, ``); rec.Code != http.StatusOK {
		t.Fatalf("before logout: want 200, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/logout", token, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: want 204, got %d", rec.Code)
	}
	// The revoked token no longer authenticates.
	if rec := do(t, s, http.MethodGet, "/api/v1/stats", token, ``); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: want 401, got %d", rec.Code)
	}
}

func TestAdminSessionExpiry(t *testing.T) {
	s := &AdminServer{Store: newAdminFake(), Username: "admin", Password: "hunter2", SessionTTL: -1}
	// A non-positive TTL falls back to the default, so the session is live.
	token := loginToken(t, s, "admin", "hunter2")
	if rec := do(t, s, http.MethodGet, "/api/v1/stats", token, ``); rec.Code != http.StatusOK {
		t.Fatalf("default ttl session: want 200, got %d", rec.Code)
	}
}

func TestAdminProgress(t *testing.T) {
	f := newAdminFake()
	f.progress = store.CycleProgress{
		Active: true, BatchID: 4, Total: 10, Done: 4, Failed: 1, Open: 5,
		StartedAt: time.Now().Add(-50 * time.Second),
	}
	s := &AdminServer{Store: f}
	rec := do(t, s, http.MethodGet, "/api/v1/progress", "", ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var v struct {
		Active     bool
		Completed  int
		Percent    float64
		EtaSeconds float64 `json:"eta_seconds"`
	}
	mustJSON(t, rec, &v)
	if !v.Active || v.Completed != 5 || v.Percent != 50 {
		t.Fatalf("progress = %+v", v)
	}
	if v.EtaSeconds <= 0 {
		t.Fatalf("expected a positive ETA, got %v", v.EtaSeconds)
	}
}

func TestAdminProgressIdle(t *testing.T) {
	f := newAdminFake()
	f.progress = store.CycleProgress{Active: false}
	s := &AdminServer{Store: f}
	rec := do(t, s, http.MethodGet, "/api/v1/progress", "", ``)
	var v struct {
		Active     bool
		EtaSeconds float64 `json:"eta_seconds"`
	}
	mustJSON(t, rec, &v)
	if v.Active || v.EtaSeconds != -1 {
		t.Fatalf("idle progress = %+v", v)
	}
}

func TestAdminLogs(t *testing.T) {
	hub := logbuf.New(10)
	hub.Write([]byte("hello world\nsecond\n"))
	s := &AdminServer{Store: newAdminFake(), Logs: hub}
	rec := do(t, s, http.MethodGet, "/api/v1/logs?since=0", "", ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var v struct {
		Lines   []logbuf.Entry `json:"lines"`
		NextSeq int64          `json:"next_seq"`
	}
	mustJSON(t, rec, &v)
	if len(v.Lines) != 2 || v.NextSeq != 2 || v.Lines[0].Msg != "hello world" {
		t.Fatalf("logs = %+v (next=%d)", v.Lines, v.NextSeq)
	}
}

func TestAdminWorkerTokenCRUD(t *testing.T) {
	f := newAdminFake()
	// No password configured: auth is dev-open, so this test exercises CRUD only.
	s := &AdminServer{Store: f}

	// Create returns the secret once.
	rec := do(t, s, http.MethodPost, "/api/v1/worker-tokens", "admin", `{"name":"home-vps"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var created struct{ Name, Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "home-vps" || created.Token == "" {
		t.Fatalf("unexpected create response: %+v", created)
	}
	if f.createdToken != "home-vps" {
		t.Fatalf("store not asked to create home-vps, got %q", f.createdToken)
	}

	// List exposes metadata but not the secret.
	now := time.Now()
	f.workerTokens = []model.WorkerToken{{ID: 7, Name: "home-vps", Enabled: true, CreatedAt: now}}
	rec = do(t, s, http.MethodGet, "/api/v1/worker-tokens", "admin", ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"name":"home-vps"`) || strings.Contains(body, "token_hash") || strings.Contains(body, `"token"`) {
		t.Fatalf("list leaked or malformed: %s", body)
	}

	// Delete revokes by id.
	rec = do(t, s, http.MethodDelete, "/api/v1/worker-tokens/7", "admin", ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", rec.Code)
	}
	if f.deletedToken != 7 {
		t.Fatalf("store not asked to delete id 7, got %d", f.deletedToken)
	}
}

func TestAdminListServersParsesFilter(t *testing.T) {
	f := newAdminFake()
	f.servers = []store.ServerSummary{{ID: 1, Protocol: model.ProtocolVLESS, Country: "FR"}}
	s := &AdminServer{Store: f}

	rec := do(t, s, http.MethodGet, "/api/v1/servers?country=FR&min_speed=5.5&worker=swift-otter-1&limit=10", "", ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if f.listFilter.Country != "FR" || f.listFilter.MinSpeed != 5.5 || f.listFilter.Worker != "swift-otter-1" || f.listFilter.Limit != 10 {
		t.Fatalf("filter not parsed: %+v", f.listFilter)
	}
	var got []store.ServerSummary
	mustJSON(t, rec, &got)
	if len(got) != 1 || got[0].Country != "FR" {
		t.Fatalf("unexpected servers: %+v", got)
	}
}

func TestAdminServerDetail(t *testing.T) {
	f := newAdminFake()
	f.server = model.Server{Protocol: model.ProtocolTrojan, Host: "h", Port: 443, Country: "CH", SeqName: "CH1", RawURI: "trojan://x"}
	f.history = []store.RunRecord{{ID: 9, WorkerID: "w1", Status: model.StatusOK}}
	s := &AdminServer{Store: f}

	rec := do(t, s, http.MethodGet, "/api/v1/servers/42", "", ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp serverDetailResp
	mustJSON(t, rec, &resp)
	if resp.Server.ID != 42 || resp.Server.RawURI != "trojan://x" || len(resp.History) != 1 || resp.History[0].WorkerID != "w1" {
		t.Fatalf("unexpected detail: %+v", resp)
	}
}

func TestAdminServerDetailBadID(t *testing.T) {
	s := &AdminServer{Store: newAdminFake()}
	if rec := do(t, s, http.MethodGet, "/api/v1/servers/abc", "", ``); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAdminServerDetailNotFound(t *testing.T) {
	f := newAdminFake()
	f.getErr = errNotFound
	s := &AdminServer{Store: f}
	if rec := do(t, s, http.MethodGet, "/api/v1/servers/99", "", ``); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestAdminPutSourceUpsert(t *testing.T) {
	f := newAdminFake()
	s := &AdminServer{Store: f}
	rec := do(t, s, http.MethodPut, "/api/v1/sources", "", `{"kind":"subscription_url","location":"https://x/sub"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(f.upserted) != 1 || f.upserted[0].Location != "https://x/sub" || f.upserted[0].Kind != model.SourceSubscriptionURL {
		t.Fatalf("upsert not recorded: %+v", f.upserted)
	}
}

func TestAdminPutSourceToggle(t *testing.T) {
	f := newAdminFake()
	s := &AdminServer{Store: f}
	rec := do(t, s, http.MethodPut, "/api/v1/sources", "", `{"id":7,"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if en, ok := f.toggled[7]; !ok || en {
		t.Fatalf("toggle not recorded: %+v", f.toggled)
	}
}

func TestAdminPutSourceRejectsBadKind(t *testing.T) {
	s := &AdminServer{Store: newAdminFake()}
	if rec := do(t, s, http.MethodPut, "/api/v1/sources", "", `{"kind":"bogus","location":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAdminPutSettings(t *testing.T) {
	f := newAdminFake()
	s := &AdminServer{Store: f}
	rec := do(t, s, http.MethodPut, "/api/v1/settings", "", `{"approval.min_dl_mbps":5,"approval.max_latency_ms":600}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if string(f.setSettings["approval.min_dl_mbps"]) != "5" || string(f.setSettings["approval.max_latency_ms"]) != "600" {
		t.Fatalf("settings not stored: %+v", f.setSettings)
	}
}

func TestAdminPutSettingsRejectsEmpty(t *testing.T) {
	s := &AdminServer{Store: newAdminFake()}
	if rec := do(t, s, http.MethodPut, "/api/v1/settings", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAdminActionTriggers(t *testing.T) {
	var fired string
	s := &AdminServer{Store: newAdminFake(), Action: func(name string) error {
		fired = name
		return nil
	}}
	rec := do(t, s, http.MethodPost, "/api/v1/actions/publish", "", ``)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d", rec.Code)
	}
	if fired != "publish" {
		t.Fatalf("action not fired: %q", fired)
	}
}

func TestAdminActionUnknown(t *testing.T) {
	s := &AdminServer{Store: newAdminFake(), Action: func(string) error { return nil }}
	if rec := do(t, s, http.MethodPost, "/api/v1/actions/nope", "", ``); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestAdminActionUnavailable(t *testing.T) {
	s := &AdminServer{Store: newAdminFake()} // no Action wired
	if rec := do(t, s, http.MethodPost, "/api/v1/actions/retest", "", ``); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}
