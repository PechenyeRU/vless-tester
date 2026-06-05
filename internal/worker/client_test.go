package worker_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/whitedns/vless-tester/internal/api"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
	"github.com/whitedns/vless-tester/internal/worker"
)

// apiFake is an in-memory api.Store so the client talks to the real api handler
// over httptest, verifying the JSON contract end to end without a database.
type apiFake struct {
	registered     map[string]model.Worker
	jobs           []store.ClaimedJob
	owned          map[int64]bool
	results        []model.TestRun
	nacked         []int64
	mediaPlatforms []string
}

func newAPIFake() *apiFake {
	return &apiFake{registered: map[string]model.Worker{}, owned: map[int64]bool{}}
}

func (f *apiFake) UpsertWorker(_ context.Context, w model.Worker) error {
	f.registered[w.ID] = w
	return nil
}
func (f *apiFake) Heartbeat(_ context.Context, _, _ string) error { return nil }
func (f *apiFake) ClaimJobs(_ context.Context, _ string, _ model.JobPhase, max int, _ []string) ([]store.ClaimedJob, error) {
	if len(f.jobs) > max {
		return f.jobs[:max], nil
	}
	return f.jobs, nil
}
func (f *apiFake) RecordResult(_ context.Context, _ string, jobID int64, r model.TestRun) (bool, error) {
	if !f.owned[jobID] {
		return false, nil
	}
	f.results = append(f.results, r)
	return true, nil
}
func (f *apiFake) NackJobs(_ context.Context, _ string, ids []int64) (int64, error) {
	f.nacked = append(f.nacked, ids...)
	return int64(len(ids)), nil
}
func (f *apiFake) MediaChecks(_ context.Context) ([]string, error)  { return f.mediaPlatforms, nil }
func (f *apiFake) MediaRequire(_ context.Context) ([]string, error) { return nil, nil }

// tokResolver authenticates one secret as one worker name.
type tokResolver struct{ token, name string }

func (r tokResolver) ResolveWorkerToken(_ context.Context, t string) (string, []string, bool, error) {
	if t == r.token {
		return r.name, nil, true, nil
	}
	return "", nil, false, nil
}

func newServer(t *testing.T, st api.Store, token string) (*worker.Client, func()) {
	t.Helper()
	srv := httptest.NewServer((&api.Server{Store: st, Tokens: tokResolver{token: token, name: "probe-1"}}).Handler())
	c := &worker.Client{BaseURL: srv.URL, Token: token, HTTP: srv.Client()}
	return c, srv.Close
}

func TestClientRegisterUsesTokenIdentity(t *testing.T) {
	f := newAPIFake()
	c, closeFn := newServer(t, f, "secret")
	defer closeFn()

	// The worker sends no id; its identity comes from the token (name "probe-1").
	id, err := c.Register(context.Background(), "", model.Capacity{Latency: 100})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id != "probe-1" {
		t.Fatalf("identity = %q, want probe-1 (token name)", id)
	}
	if _, ok := f.registered["probe-1"]; !ok {
		t.Fatal("worker not stored under token name")
	}
}

func TestClientRegisterRejectsBadToken(t *testing.T) {
	f := newAPIFake()
	srv := httptest.NewServer((&api.Server{Store: f, Tokens: tokResolver{token: "secret", name: "probe-1"}}).Handler())
	defer srv.Close()
	c := &worker.Client{BaseURL: srv.URL, Token: "wrong", HTTP: srv.Client()}

	if _, err := c.Register(context.Background(), "w1", model.Capacity{}); err == nil {
		t.Fatal("expected auth failure with wrong token")
	}
}

func TestClientClaimReportNack(t *testing.T) {
	f := newAPIFake()
	f.jobs = []store.ClaimedJob{
		{JobID: 1, ServerID: 10, RawURI: "vless://a", Phase: model.PhaseLatency, Protocol: model.ProtocolVLESS},
		{JobID: 2, ServerID: 11, RawURI: "vless://b", Phase: model.PhaseLatency, Protocol: model.ProtocolVLESS},
	}
	f.owned[1] = true // only job 1 is reportable
	c, closeFn := newServer(t, f, "secret")
	defer closeFn()
	ctx := context.Background()

	jobs, err := c.Claim(ctx, "w1", model.PhaseLatency, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(jobs) != 2 || jobs[0].RawURI != "vless://a" || jobs[0].Protocol != "vless" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}

	lat := 33
	accepted, err := c.Report(ctx, "w1", []worker.Result{
		{JobID: 1, Status: "ok", LatencyMs: &lat},
		{JobID: 2, Status: "ok", LatencyMs: &lat},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if accepted != 1 {
		t.Fatalf("accepted = %d, want 1 (only owned job)", accepted)
	}
	if len(f.results) != 1 || *f.results[0].LatencyMs != 33 {
		t.Fatalf("results: %+v", f.results)
	}

	if err := c.Nack(ctx, "w1", []int64{2}); err != nil {
		t.Fatalf("nack: %v", err)
	}
	if len(f.nacked) != 1 || f.nacked[0] != 2 {
		t.Fatalf("nacked: %+v", f.nacked)
	}
}

func TestClientHeartbeat(t *testing.T) {
	f := newAPIFake()
	c, closeFn := newServer(t, f, "secret")
	defer closeFn()
	if err := c.Heartbeat(context.Background(), "w1", "busy", model.Capacity{Speed: 2}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
}
