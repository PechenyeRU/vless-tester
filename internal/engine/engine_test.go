package engine_test

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/engine"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
	"github.com/whitedns/vless-tester/internal/output"
	"github.com/whitedns/vless-tester/internal/store"
)

// stubInstance / stubProber stand in for the mihomo core: no real proxy is
// started, so the pipeline can be exercised without a core or live servers. The
// instance hands back a client that talks to the test HTTP server directly.
type stubInstance struct{ client *http.Client }

func (s stubInstance) Client() *http.Client { return s.client }
func (stubInstance) Close() error           { return nil }

type stubProber struct{ client *http.Client }

func (p stubProber) Start(_ context.Context, _ model.Server) (engine.Instance, error) {
	return stubInstance(p), nil
}

// fakeResolver maps IPs to fixed countries.
type fakeResolver map[string]string

func (f fakeResolver) LookupCountry(ip net.IP) (string, error) { return f[ip.String()], nil }

func newSpeedServer(_ *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/204", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		io.CopyN(w, zeroSrc{}, int64(n))
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	})
	return httptest.NewServer(mux)
}

type zeroSrc struct{}

func (zeroSrc) Read(p []byte) (int, error) { return len(p), nil }

// dbTestLock matches the key used by the store package so DB integration tests
// serialize across packages sharing one database.
const dbTestLock = 913551

func lockDB(t *testing.T, dsn string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		return
	}
	if _, err := conn.Exec(context.Background(), "SELECT pg_advisory_lock($1)", dbTestLock); err != nil {
		conn.Close(context.Background())
		return
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", dbTestLock)
		conn.Close(context.Background())
	})
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping engine integration test")
	}
	lockDB(t, dsn)
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_DATABASE_URL: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`TRUNCATE checks, test_runs, batches, jobs, country_seq, servers, workers, sources RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Settings persist across tests (not truncated); reset the ones tests mutate
	// so each engine test starts from an unrestricted, deterministic baseline.
	_ = st.SetSetting(ctx, "protocols.enabled", []string{})
	_ = st.SetSetting(ctx, "output.node_prefix", "")
	_ = st.SetSetting(ctx, "output.success_limit", 0)
	_ = st.SetSetting(ctx, "filter.name_include", "")
	_ = st.SetSetting(ctx, "filter.name_exclude", "")
	_ = st.SetSetting(ctx, "dispatch.shuffle", false)
	_ = st.SetSetting(ctx, "dispatch.max_probes", 0)
	t.Cleanup(st.Close)
	return st
}

func newEngine(st *store.Store, srv *httptest.Server) *engine.Engine {
	return &engine.Engine{
		Store: st,
		// The stub instance hands back a client that talks to the test server.
		Prober:  stubProber{client: srv.Client()},
		Latency: checks.LatencyCheck{URL: srv.URL + "/204"},
		Speed: checks.SpeedCheck{Config: checks.SpeedConfig{
			DownloadURL: srv.URL + "/down",
			UploadURL:   srv.URL + "/up",
			Streams:     2,
			Bytes:       200_000,
		}},
		Resolver:  fakeResolver{"8.8.8.8": "FR", "1.1.1.1": "FR"},
		Seq:       naming.Allocator{Backend: st.NewSeqBackend()},
		Publisher: &output.MockPublisher{},
		Brand:     "@WhiteDNS",
		WorkerID:  "test-worker",
		Approval:  engine.Approval{MaxLatencyMs: 60000, MinDlMBps: 0},
	}
}

func TestEngineRunOnceEndToEnd(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()

	eng := newEngine(st, srv)
	pub := eng.Publisher.(*output.MockPublisher)

	servers, _ := ingest.ParseList(strings.Join([]string{
		"vless://uuid@8.8.8.8:443?type=ws#a",
		"trojan://pw@1.1.1.1:443#b",
	}, "\n"))

	ctx := context.Background()
	sum, err := eng.RunOnce(ctx, servers)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if sum.Tested != 2 || sum.Approved != 2 {
		t.Fatalf("tested=%d approved=%d, want 2/2", sum.Tested, sum.Approved)
	}

	// Servers persisted with stable sequence names.
	n, _ := st.CountServers(ctx)
	if n != 2 {
		t.Fatalf("stored servers = %d, want 2", n)
	}
	s0, _ := st.GetServer(ctx, 1)
	s1, _ := st.GetServer(ctx, 2)
	if s0.SeqName != "FR1" || s1.SeqName != "FR2" {
		t.Fatalf("seq names = %q,%q want FR1,FR2", s0.SeqName, s1.SeqName)
	}
	if s0.Country != "FR" {
		t.Fatalf("country = %q, want FR", s0.Country)
	}

	// Publisher received artifacts containing two renamed links.
	if pub.Calls != 1 {
		t.Fatalf("publisher calls = %d, want 1", pub.Calls)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(pub.Files[output.FileSubscription]))
	if err != nil {
		t.Fatalf("subscription not base64: %v", err)
	}
	if got := strings.Count(string(decoded), "\n"); got != 1 {
		t.Fatalf("expected 2 links (1 newline), got %d newlines", got)
	}
	if !strings.Contains(string(decoded), "@WhiteDNS | FR1|") {
		t.Fatalf("renamed node name missing from subscription")
	}

	// Multi-format subscriptions were rendered and persisted for /sub. The clash
	// artifact must be valid yaml carrying both approved nodes.
	clash, err := st.PublishedArtifact(ctx, "clash")
	if err != nil {
		t.Fatalf("clash artifact not persisted: %v", err)
	}
	if clash.NodeCount != 2 {
		t.Fatalf("clash node_count = %d, want 2", clash.NodeCount)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(clash.Content, &doc); err != nil {
		t.Fatalf("persisted clash is not valid yaml: %v", err)
	}
	if len(doc.Proxies) != 2 {
		t.Fatalf("clash proxies = %d, want 2", len(doc.Proxies))
	}
	for _, target := range []string{"base64", "singbox", "v2ray", "surge"} {
		if _, err := st.PublishedArtifact(ctx, target); err != nil {
			t.Fatalf("artifact %s not persisted: %v", target, err)
		}
	}
}

type mockNotifier struct{ msgs []string }

func (m *mockNotifier) Notify(_ context.Context, msg string) error {
	m.msgs = append(m.msgs, msg)
	return nil
}

func TestEngineNotifiesOnPublish(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()

	eng := newEngine(st, srv)
	mn := &mockNotifier{}
	eng.Notifier = mn

	servers, _ := ingest.ParseList("vless://uuid@8.8.8.8:443?type=ws#a")
	sum, err := eng.RunOnce(context.Background(), servers)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(mn.msgs) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(mn.msgs))
	}
	if !strings.Contains(mn.msgs[0], "working servers") {
		t.Fatalf("notify message = %q", mn.msgs[0])
	}
	// The summary tallies per country (FR from the fake resolver).
	if sum.ByCountry["FR"] != 1 {
		t.Fatalf("by-country = %v, want FR:1", sum.ByCountry)
	}
}

// fakeLive is a settings-backed LiveSettings stand-in for testing live re-gating.
type fakeLive struct {
	ap            engine.Approval
	notifyEnabled bool
	notifyURLs    []string
}

func (f *fakeLive) Approval(context.Context) engine.Approval    { return f.ap }
func (f *fakeLive) Fanout(context.Context) int                  { return 1 }
func (f *fakeLive) LeaseTTL(context.Context) time.Duration      { return 2 * time.Minute }
func (f *fakeLive) MaxAttempts(context.Context) int             { return 3 }
func (f *fakeLive) NotifyURLs(context.Context) (bool, []string) { return f.notifyEnabled, f.notifyURLs }

func TestEngineLiveApprovalGate(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()

	eng := newEngine(st, srv)
	servers, _ := ingest.ParseList("vless://uuid@8.8.8.8:443?type=ws#a")
	if _, err := eng.RunOnce(context.Background(), servers); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// From here on no proxy test may run: re-gating reads the live settings only.
	eng.Prober = failProber{}

	// A strict live gate (impossibly high min download) approves nothing.
	eng.Live = &fakeLive{ap: engine.Approval{MaxLatencyMs: 5000, MinDlMBps: 1e9, AllowPartial: true}}
	sum, err := eng.PublishFromHistory(context.Background())
	if err != nil {
		t.Fatalf("publish strict: %v", err)
	}
	if sum.Approved != 0 {
		t.Fatalf("strict live gate approved %d, want 0", sum.Approved)
	}

	// Relaxing the live gate re-approves without any retest.
	eng.Live = &fakeLive{ap: engine.Approval{MaxLatencyMs: 5000, MinDlMBps: 0, AllowPartial: true}}
	sum, err = eng.PublishFromHistory(context.Background())
	if err != nil {
		t.Fatalf("publish relaxed: %v", err)
	}
	if sum.Approved != 1 {
		t.Fatalf("relaxed live gate approved %d, want 1", sum.Approved)
	}
}

func TestEngineOutputFilters(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()
	ctx := context.Background()

	eng := newEngine(st, srv)
	servers, _ := ingest.ParseList(strings.Join([]string{
		"vless://uuid@8.8.8.8:443?type=ws#a",
		"trojan://pw@1.1.1.1:443#b",
	}, "\n"))
	if _, err := eng.RunOnce(ctx, servers); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	eng.Prober = failProber{} // re-publish must not retest

	// Exclude FR2 by name regex -> only FR1 remains.
	_ = st.SetSetting(ctx, "filter.name_exclude", "FR2")
	sum, err := eng.PublishFromHistory(ctx)
	if err != nil {
		t.Fatalf("publish exclude: %v", err)
	}
	if sum.Approved != 1 {
		t.Fatalf("name exclude approved %d, want 1", sum.Approved)
	}

	// success-limit caps the published count.
	_ = st.SetSetting(ctx, "filter.name_exclude", "")
	_ = st.SetSetting(ctx, "output.success_limit", 1)
	sum, err = eng.PublishFromHistory(ctx)
	if err != nil {
		t.Fatalf("publish limit: %v", err)
	}
	if sum.Approved != 1 {
		t.Fatalf("success-limit approved %d, want 1", sum.Approved)
	}
}

func TestEngineStableSeqAcrossRuns(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()

	eng := newEngine(st, srv)
	servers, _ := ingest.ParseList("vless://uuid@8.8.8.8:443?type=ws#a")

	ctx := context.Background()
	if _, err := eng.RunOnce(ctx, servers); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, _ := st.GetServer(ctx, 1)

	// A second run must keep the same sequence name (stable identity).
	if _, err := eng.RunOnce(ctx, servers); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, _ := st.GetServer(ctx, 1)
	if first.SeqName != second.SeqName || first.SeqName != "FR1" {
		t.Fatalf("seq drifted: %q -> %q", first.SeqName, second.SeqName)
	}
}

// TestEngineRegateWithoutRetest proves the history-driven gate: after one test
// cycle, raising the speed threshold and re-publishing changes the approved set
// without running any new proxy test (the prober is swapped for one that fails
// if called).
func TestEngineRegateWithoutRetest(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()

	eng := newEngine(st, srv)
	eng.Speed = checks.SpeedCheck{Config: checks.SpeedConfig{
		DownloadURL: srv.URL + "/down",
		UploadURL:   srv.URL + "/up",
		Streams:     2,
		Bytes:       200_000,
	}}
	eng.Approval = engine.Approval{MaxLatencyMs: 60000, MinDlMBps: 0}

	servers, _ := ingest.ParseList(strings.Join([]string{
		"vless://uuid@8.8.8.8:443?type=ws#a",
		"trojan://pw@1.1.1.1:443#b",
	}, "\n"))

	ctx := context.Background()
	first, err := eng.RunOnce(ctx, servers)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if first.Approved != 2 {
		t.Fatalf("initial approved = %d, want 2", first.Approved)
	}

	// Make any further proxy test fail, then re-gate with an impossibly high
	// speed bar and republish purely from history.
	eng.Prober = failProber{}
	eng.Approval = engine.Approval{MaxLatencyMs: 60000, MinDlMBps: 1e9}

	regated, err := eng.PublishFromHistory(ctx)
	if err != nil {
		t.Fatalf("PublishFromHistory: %v", err)
	}
	if regated.Approved != 0 {
		t.Fatalf("re-gated approved = %d, want 0 (no server beats 1e9 MB/s)", regated.Approved)
	}

	// Lowering the bar again republishes the full set, still without testing.
	eng.Approval = engine.Approval{MaxLatencyMs: 60000, MinDlMBps: 0}
	back, err := eng.PublishFromHistory(ctx)
	if err != nil {
		t.Fatalf("PublishFromHistory (low gate): %v", err)
	}
	if back.Approved != 2 {
		t.Fatalf("restored approved = %d, want 2", back.Approved)
	}
}

// failProber fails if Start is ever called, guarding that PublishFromHistory
// performs no proxy tests.
type failProber struct{}

func (failProber) Start(context.Context, model.Server) (engine.Instance, error) {
	return nil, errNoTest
}

var errNoTest = errTest("prober must not be called during history-only publish")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestEngineLatencyFailSkipsApproval(t *testing.T) {
	st := newTestStore(t)
	srv := newSpeedServer(t)
	defer srv.Close()

	eng := newEngine(st, srv)
	// Point latency at a closed port so the check fails.
	eng.Latency = checks.LatencyCheck{URL: "http://127.0.0.1:1/nope"}

	servers, _ := ingest.ParseList("vless://uuid@8.8.8.8:443?type=ws#a")
	ctx := context.Background()
	sum, err := eng.RunOnce(ctx, servers)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if sum.Tested != 1 || sum.Approved != 0 {
		t.Fatalf("tested=%d approved=%d, want 1/0", sum.Tested, sum.Approved)
	}
}
