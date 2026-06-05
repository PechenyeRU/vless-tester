package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

// fakeStore is an in-memory stand-in for *store.Store so the handlers run with
// no database. It records calls and lets each test script the outcomes.
type fakeStore struct {
	workers   map[string]model.Worker
	heartbeat map[string]string
	claimOut  []store.ClaimedJob
	claimArgs struct {
		worker    string
		phase     model.JobPhase
		max       int
		protocols []string
	}
	// owned holds job ids this worker legitimately claimed; RecordResult/NackJobs
	// only act on those, mirroring the real ownership check.
	owned          map[int64]bool
	recorded       []model.TestRun
	nacked         []int64
	mediaPlatforms []string
	mediaRequire   []string
	ipRisk         bool
}

func newFake() *fakeStore {
	return &fakeStore{
		workers:   map[string]model.Worker{},
		heartbeat: map[string]string{},
		owned:     map[int64]bool{},
	}
}

func (f *fakeStore) UpsertWorker(_ context.Context, w model.Worker) error {
	f.workers[w.ID] = w
	return nil
}

func (f *fakeStore) Heartbeat(_ context.Context, workerID, status string) error {
	f.heartbeat[workerID] = status
	return nil
}

func (f *fakeStore) ClaimJobs(_ context.Context, workerID string, phase model.JobPhase, max int, protocols []string) ([]store.ClaimedJob, error) {
	f.claimArgs.worker = workerID
	f.claimArgs.phase = phase
	f.claimArgs.max = max
	f.claimArgs.protocols = protocols
	return f.claimOut, nil
}

func (f *fakeStore) RecordResult(_ context.Context, workerID string, jobID int64, r model.TestRun) (bool, error) {
	if !f.owned[jobID] {
		return false, nil
	}
	r.WorkerID = workerID
	f.recorded = append(f.recorded, r)
	return true, nil
}

func (f *fakeStore) NackJobs(_ context.Context, workerID string, jobIDs []int64) (int64, error) {
	var n int64
	for _, id := range jobIDs {
		if f.owned[id] {
			f.nacked = append(f.nacked, id)
			n++
		}
	}
	return n, nil
}
func (f *fakeStore) MediaChecks(_ context.Context) ([]string, error)  { return f.mediaPlatforms, nil }
func (f *fakeStore) MediaRequire(_ context.Context) ([]string, error) { return f.mediaRequire, nil }
func (f *fakeStore) IPRiskEnabled(_ context.Context) (bool, error)    { return f.ipRisk, nil }

// handlerProvider is satisfied by both *Server (worker plane) and *AdminServer
// (admin plane), so the do() helper drives either.
type handlerProvider interface{ Handler() http.Handler }

// do issues a request against the server's handler and returns the recorder.
func do(t *testing.T, s handlerProvider, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAuth(t *testing.T) {
	s := &Server{Store: newFake(), Tokens: fakeTokens{secret: "wt_good", name: "probe-1"}}

	if rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "wrong", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "wt_good", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", rec.Code)
	}
}

func TestAuthDisabledWhenNoTokensConfigured(t *testing.T) {
	s := &Server{Store: newFake()} // no resolver: dev mode, open
	if rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("dev mode: want 200, got %d", rec.Code)
	}
}

func TestRegisterGeneratesMnemonic(t *testing.T) {
	f := newFake()
	s := &Server{Store: f}

	rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "", `{"capacity":{"latency":100}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp registerResp
	mustJSON(t, rec, &resp)
	if !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(resp.ID) {
		t.Fatalf("generated id %q does not match ^[A-Za-z0-9-]+$", resp.ID)
	}
	if _, ok := f.workers[resp.ID]; !ok {
		t.Fatalf("worker %q not upserted", resp.ID)
	}
	if f.workers[resp.ID].Capacity.Latency != 100 {
		t.Fatalf("capacity not stored: %+v", f.workers[resp.ID].Capacity)
	}
}

// fakeTokens resolves exactly one secret to one worker name (with optional
// per-worker protocols).
type fakeTokens struct {
	secret, name string
	protocols    []string
}

func (f fakeTokens) ResolveWorkerToken(_ context.Context, token string) (string, []string, bool, error) {
	if token == f.secret {
		return f.name, f.protocols, true, nil
	}
	return "", nil, false, nil
}

func TestPerWorkerTokenAuthAndIdentity(t *testing.T) {
	f := newFake()
	s := &Server{Store: f, Tokens: fakeTokens{secret: "wt_abc", name: "home-vps"}}

	// An unknown token is rejected.
	if rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "nope", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token: want 401, got %d", rec.Code)
	}

	// The valid token authenticates and pins identity: even a forged body id is
	// ignored in favor of the token's worker name.
	rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "wt_abc", `{"id":"attacker","capacity":{"latency":50}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", rec.Code)
	}
	var resp registerResp
	mustJSON(t, rec, &resp)
	if resp.ID != "home-vps" {
		t.Fatalf("identity = %q, want home-vps (token name wins)", resp.ID)
	}
	if _, ok := f.workers["home-vps"]; !ok {
		t.Fatal("worker not stored under token name")
	}
	if _, ok := f.workers["attacker"]; ok {
		t.Fatal("client-supplied id must be ignored under a per-worker token")
	}
}

func TestRegisterHonorsProvidedID(t *testing.T) {
	f := newFake()
	s := &Server{Store: f}
	rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "", `{"id":"swift-otter-1"}`)
	var resp registerResp
	mustJSON(t, rec, &resp)
	if resp.ID != "swift-otter-1" {
		t.Fatalf("want provided id, got %q", resp.ID)
	}
}

func TestHeartbeat(t *testing.T) {
	f := newFake()
	s := &Server{Store: f}

	if rec := do(t, s, http.MethodPost, "/api/v1/workers/heartbeat", "", `{"status":"busy"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id: want 400, got %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/api/v1/workers/heartbeat", "", `{"id":"w1","status":"busy"}`); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if f.heartbeat["w1"] != "busy" {
		t.Fatalf("heartbeat status not recorded: %q", f.heartbeat["w1"])
	}
}

func TestClaim(t *testing.T) {
	f := newFake()
	f.claimOut = []store.ClaimedJob{
		{JobID: 7, ServerID: 3, RawURI: "vless://x", Phase: model.PhaseLatency, Protocol: model.ProtocolVLESS},
	}
	s := &Server{Store: f}

	rec := do(t, s, http.MethodPost, "/api/v1/jobs/claim", "", `{"worker_id":"w1","phase":"latency","max":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var jobs []claimedJob
	mustJSON(t, rec, &jobs)
	if len(jobs) != 1 || jobs[0].JobID != 7 || jobs[0].RawURI != "vless://x" || jobs[0].Protocol != "vless" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
	if f.claimArgs.worker != "w1" || f.claimArgs.phase != model.PhaseLatency || f.claimArgs.max != 5 {
		t.Fatalf("claim args: %+v", f.claimArgs)
	}
}

func TestClaimPushesIPRiskFlag(t *testing.T) {
	f := newFake()
	f.claimOut = []store.ClaimedJob{
		{JobID: 7, ServerID: 3, RawURI: "vless://x", Phase: model.PhaseLatency, Protocol: model.ProtocolVLESS},
	}
	f.ipRisk = true
	s := &Server{Store: f}

	rec := do(t, s, http.MethodPost, "/api/v1/jobs/claim", "", `{"worker_id":"w1","phase":"latency","max":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var jobs []claimedJob
	mustJSON(t, rec, &jobs)
	if len(jobs) != 1 || !jobs[0].IPRisk {
		t.Fatalf("expected ip_risk flag on claimed job, got %+v", jobs)
	}
}

func TestClaimRequiresWorkerID(t *testing.T) {
	s := &Server{Store: newFake()}
	if rec := do(t, s, http.MethodPost, "/api/v1/jobs/claim", "", `{"phase":"latency"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestClaimRejectsInvalidPhase(t *testing.T) {
	s := &Server{Store: newFake()}
	if rec := do(t, s, http.MethodPost, "/api/v1/jobs/claim", "", `{"worker_id":"w1","phase":"bogus"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestClaimClampsMax(t *testing.T) {
	f := newFake()
	s := &Server{Store: f}

	do(t, s, http.MethodPost, "/api/v1/jobs/claim", "", `{"worker_id":"w1"}`)
	if f.claimArgs.max != defaultClaim {
		t.Fatalf("default max: want %d, got %d", defaultClaim, f.claimArgs.max)
	}
	do(t, s, http.MethodPost, "/api/v1/jobs/claim", "", `{"worker_id":"w1","max":99999}`)
	if f.claimArgs.max != maxClaim {
		t.Fatalf("capped max: want %d, got %d", maxClaim, f.claimArgs.max)
	}
}

func TestResultsRecordsOwnedDropsForeign(t *testing.T) {
	f := newFake()
	f.owned[1] = true // job 1 is held by this worker; job 2 is not
	s := &Server{Store: f}

	body := `{"worker_id":"w1","results":[
		{"job_id":1,"status":"ok","latency_ms":42,"dl_mbps":12.5},
		{"job_id":2,"status":"ok","latency_ms":10}
	]}`
	rec := do(t, s, http.MethodPost, "/api/v1/jobs/results", "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp resultsResp
	mustJSON(t, rec, &resp)
	if resp.Accepted != 1 {
		t.Fatalf("accepted: want 1, got %d", resp.Accepted)
	}
	if len(f.recorded) != 1 || f.recorded[0].Status != model.StatusOK || *f.recorded[0].LatencyMs != 42 {
		t.Fatalf("recorded: %+v", f.recorded)
	}
}

func TestResultsNormalizesUnknownStatus(t *testing.T) {
	f := newFake()
	f.owned[1] = true
	s := &Server{Store: f}
	do(t, s, http.MethodPost, "/api/v1/jobs/results", "", `{"worker_id":"w1","results":[{"job_id":1,"status":"weird"}]}`)
	if len(f.recorded) != 1 || f.recorded[0].Status != model.StatusError {
		t.Fatalf("unknown status should normalize to error: %+v", f.recorded)
	}
}

func TestNack(t *testing.T) {
	f := newFake()
	f.owned[5] = true
	s := &Server{Store: f}

	rec := do(t, s, http.MethodPost, "/api/v1/jobs/nack", "", `{"worker_id":"w1","job_ids":[5,6]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(f.nacked) != 1 || f.nacked[0] != 5 {
		t.Fatalf("nacked: %+v", f.nacked)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := &Server{Store: newFake()}
	if rec := do(t, s, http.MethodGet, "/api/v1/jobs/claim", "", ``); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

func TestInvalidJSON(t *testing.T) {
	s := &Server{Store: newFake()}
	if rec := do(t, s, http.MethodPost, "/api/v1/workers/register", "", `{not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func mustJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(dst); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
}
