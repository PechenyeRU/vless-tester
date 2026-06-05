package store_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

// testDSN returns the integration database URL, or empty when not configured.
func testDSN() string { return os.Getenv("TEST_DATABASE_URL") }

// dbTestLock is the advisory-lock key shared by all DB integration tests.
const dbTestLock = 913551

// lockDB serializes DB-backed tests across packages that share one database
// (their TRUNCATEs would otherwise clobber each other under parallel package
// execution). It holds a session advisory lock for the duration of the test.
func lockDB(t *testing.T, dsn string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		return // best effort; serial runs (-p 1) still work
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

// newTestStore connects to TEST_DATABASE_URL, migrates, and returns a store with
// a clean slate. It skips (not fails) when no database is reachable, so the unit
// suite stays green on machines without Postgres.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping store integration tests")
	}
	lockDB(t, dsn)
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach TEST_DATABASE_URL, skipping: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Clean every table except settings (seeded by migration and shared).
	if _, err := st.Pool().Exec(ctx,
		`TRUNCATE checks, test_runs, jobs, servers, workers, sources, worker_tokens, published_artifacts RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Settings persist across tests (not truncated); reset the ones tests mutate
	// to deterministic defaults so suites stay isolated.
	_ = st.SetSetting(ctx, "protocols.enabled", []string{})
	_ = st.SetSetting(ctx, "media.enabled", false)
	_ = st.SetSetting(ctx, "media.require", []string{})
	_ = st.SetSetting(ctx, "iprisk.enabled", false)
	t.Cleanup(st.Close)
	return st
}

func sampleServer(i int) model.Server {
	return model.Server{
		Fingerprint: fmt.Sprintf("fp-%d", i),
		RawURI:      fmt.Sprintf("vless://uuid@host%d:443", i),
		Protocol:    model.ProtocolVLESS,
		Host:        fmt.Sprintf("host%d", i),
		Port:        443,
	}
}

func TestMigrateIdempotentAndSeed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Re-running migrations must be a no-op.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var streams int
	if err := st.GetSetting(ctx, "speed.streams", &streams); err != nil {
		t.Fatalf("get speed.streams: %v", err)
	}
	if streams != 6 {
		t.Fatalf("speed.streams = %d, want 6", streams)
	}

	var adaptive bool
	if err := st.GetSetting(ctx, "speed.adaptive", &adaptive); err != nil {
		t.Fatalf("get speed.adaptive: %v", err)
	}
	if !adaptive {
		t.Fatal("speed.adaptive should default to true")
	}
}

func TestUpsertServerDedup(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id1, err := st.UpsertServer(ctx, sampleServer(1))
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	id2, err := st.UpsertServer(ctx, sampleServer(1)) // same fingerprint
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("dedup failed: ids %d != %d", id1, id2)
	}
	n, err := st.CountServers(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("server count = %d, want 1", n)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Use a throwaway key so we never clobber shared seeded settings.
	const key = "test.roundtrip"
	if err := st.SetSetting(ctx, key, map[string]int{"a": 7}); err != nil {
		t.Fatalf("set: %v", err)
	}
	var got map[string]int
	if err := st.GetSetting(ctx, key, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["a"] != 7 {
		t.Fatalf("roundtrip = %v, want a=7", got)
	}
}

func TestEnqueueJobDedupOpen(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	srvID, err := st.UpsertServer(ctx, sampleServer(1))
	if err != nil {
		t.Fatalf("upsert server: %v", err)
	}
	created, err := st.EnqueueJob(ctx, srvID, model.PhaseLatency)
	if err != nil || !created {
		t.Fatalf("first enqueue created=%v err=%v", created, err)
	}
	created, err = st.EnqueueJob(ctx, srvID, model.PhaseLatency)
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if created {
		t.Fatal("second enqueue should be a no-op while a job is open")
	}
	queued, err := st.CountJobs(ctx, model.JobQueued)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}
}

func TestRequeueExpired(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	if _, err := st.EnqueueJob(ctx, srvID, model.PhaseLatency); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := st.ClaimJobs(ctx, "w1", model.PhaseLatency, 10, nil)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim got %d err=%v", len(claimed), err)
	}
	// ttl=0 requeues anything claimed in the past; maxAttempts=0 disables capping.
	n, failed, err := st.RequeueExpired(ctx, 0, 0)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 || failed != 0 {
		t.Fatalf("requeued=%d failed=%d, want 1/0", n, failed)
	}
	queued, _ := st.CountJobs(ctx, model.JobQueued)
	if queued != 1 {
		t.Fatalf("queued after requeue = %d, want 1", queued)
	}
}

func TestInsertTestRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	lat := 42
	dl := 12.3
	id, err := st.InsertTestRun(ctx, model.TestRun{
		ServerID:  srvID,
		WorkerID:  "w1",
		Phase:     model.PhaseSpeed,
		LatencyMs: &lat,
		DlMbps:    &dl,
		Status:    model.StatusOK,
	})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero run id")
	}
}

// TestClaimJobsSkipLockedNoDoubleClaim runs several workers claiming the same
// queue concurrently and asserts every job is claimed exactly once.
func TestClaimJobsSkipLockedNoDoubleClaim(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	mustWorker(t, st, "w2")

	const n = 60
	for i := 0; i < n; i++ {
		id, err := st.UpsertServer(ctx, sampleServer(i))
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if _, err := st.EnqueueJob(ctx, id, model.PhaseLatency); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	var (
		mu      sync.Mutex
		seen    = make(map[int64]bool)
		errs    = make(chan error, 8)
		wg      sync.WaitGroup
		workers = []string{"w1", "w2", "w1", "w2"}
	)
	for _, w := range workers {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				jobs, err := st.ClaimJobs(ctx, worker, model.PhaseLatency, 4, nil)
				if err != nil {
					errs <- err
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					if seen[j.JobID] {
						errs <- fmt.Errorf("job %d claimed twice", j.JobID)
					}
					seen[j.JobID] = true
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(seen) != n {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	queued, _ := st.CountJobs(ctx, model.JobQueued)
	if queued != 0 {
		t.Fatalf("queued remaining = %d, want 0", queued)
	}
}

// TestRecordResult verifies a worker can only report results for jobs it holds:
// an owned job is recorded and closed, a job claimed by another worker is
// dropped (ok=false) and left untouched.
func TestRecordResult(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	mustWorker(t, st, "w2")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	if _, err := st.EnqueueJob(ctx, srvID, model.PhaseLatency); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := st.ClaimJobs(ctx, "w1", model.PhaseLatency, 1, nil)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim got %d err=%v", len(claimed), err)
	}
	jobID := claimed[0].JobID

	// A different worker cannot report on w1's job.
	lat := 7
	ok, err := st.RecordResult(ctx, "w2", jobID, model.TestRun{Status: model.StatusOK, LatencyMs: &lat})
	if err != nil {
		t.Fatalf("foreign record: %v", err)
	}
	if ok {
		t.Fatal("foreign worker should not record result")
	}

	// The owning worker records and the job closes as done.
	lat2 := 42
	ok, err = st.RecordResult(ctx, "w1", jobID, model.TestRun{Status: model.StatusOK, LatencyMs: &lat2})
	if err != nil {
		t.Fatalf("owner record: %v", err)
	}
	if !ok {
		t.Fatal("owner record should succeed")
	}
	done, _ := st.CountJobs(ctx, model.JobDone)
	if done != 1 {
		t.Fatalf("done jobs = %d, want 1", done)
	}

	// A failing status closes the job as failed instead.
	if _, err := st.EnqueueJob(ctx, srvID, model.PhaseSpeed); err != nil {
		t.Fatalf("enqueue speed: %v", err)
	}
	c2, _ := st.ClaimJobs(ctx, "w1", model.PhaseSpeed, 1, nil)
	if _, err := st.RecordResult(ctx, "w1", c2[0].JobID, model.TestRun{Status: model.StatusTimeout}); err != nil {
		t.Fatalf("record fail: %v", err)
	}
	failed, _ := st.CountJobs(ctx, model.JobFailed)
	if failed != 1 {
		t.Fatalf("failed jobs = %d, want 1", failed)
	}
}

// TestNackJobs verifies a worker releases only its own claimed jobs.
func TestNackJobs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	mustWorker(t, st, "w2")
	id1, _ := st.UpsertServer(ctx, sampleServer(1))
	id2, _ := st.UpsertServer(ctx, sampleServer(2))
	st.EnqueueJob(ctx, id1, model.PhaseLatency)
	st.EnqueueJob(ctx, id2, model.PhaseLatency)

	c1, _ := st.ClaimJobs(ctx, "w1", model.PhaseLatency, 1, nil)
	c2, _ := st.ClaimJobs(ctx, "w2", model.PhaseLatency, 1, nil)
	if len(c1) != 1 || len(c2) != 1 {
		t.Fatalf("claims: w1=%d w2=%d", len(c1), len(c2))
	}

	// w1 tries to release both its own and w2's job; only its own is requeued.
	n, err := st.NackJobs(ctx, "w1", []int64{c1[0].JobID, c2[0].JobID})
	if err != nil {
		t.Fatalf("nack: %v", err)
	}
	if n != 1 {
		t.Fatalf("nacked %d, want 1", n)
	}
	queued, _ := st.CountJobs(ctx, model.JobQueued)
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}
	claimedCount, _ := st.CountJobs(ctx, model.JobClaimed)
	if claimedCount != 1 {
		t.Fatalf("claimed = %d, want 1 (w2 untouched)", claimedCount)
	}
}

// recordPass claims one funnel slot for the worker and reports a passing run.
func recordPass(t *testing.T, st *store.Store, worker string, serverID int64, latency int, dl float64) {
	t.Helper()
	ctx := context.Background()
	claimed, err := st.ClaimJobs(ctx, worker, model.PhaseFunnel, 1, nil)
	if err != nil {
		t.Fatalf("claim for %s: %v", worker, err)
	}
	if len(claimed) != 1 {
		t.Fatalf("worker %s claimed %d jobs, want 1", worker, len(claimed))
	}
	ok, err := st.RecordResult(ctx, worker, claimed[0].JobID, model.TestRun{
		Status: model.StatusOK, LatencyMs: &latency, DlMbps: &dl,
	})
	if err != nil || !ok {
		t.Fatalf("record for %s: ok=%v err=%v", worker, ok, err)
	}
}

func TestEnqueueFanoutCreatesSlots(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	batch, _ := st.CreateBatch(ctx, "scheduled")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))

	n, err := st.EnqueueFanout(ctx, batch, srvID, model.PhaseFunnel, 3)
	if err != nil {
		t.Fatalf("fanout: %v", err)
	}
	if n != 3 {
		t.Fatalf("created %d slots, want 3", n)
	}
	// Re-dispatch is idempotent: the open slots already exist.
	again, _ := st.EnqueueFanout(ctx, batch, srvID, model.PhaseFunnel, 3)
	if again != 0 {
		t.Fatalf("re-dispatch created %d, want 0", again)
	}
	queued, _ := st.CountJobs(ctx, model.JobQueued)
	if queued != 3 {
		t.Fatalf("queued = %d, want 3", queued)
	}
}

// TestFanoutDistinctWorkers proves each config is tested by distinct workers: a
// worker never claims two slots of the same (server, phase).
func TestFanoutDistinctWorkers(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	mustWorker(t, st, "w2")
	batch, _ := st.CreateBatch(ctx, "scheduled")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	if _, err := st.EnqueueFanout(ctx, batch, srvID, model.PhaseFunnel, 2); err != nil {
		t.Fatalf("fanout: %v", err)
	}

	// w1 grabs one slot; asking for many more yields nothing (its only sibling is
	// the slot it already holds).
	first, _ := st.ClaimJobs(ctx, "w1", model.PhaseFunnel, 10, nil)
	if len(first) != 1 {
		t.Fatalf("w1 first claim = %d, want 1", len(first))
	}
	more, _ := st.ClaimJobs(ctx, "w1", model.PhaseFunnel, 10, nil)
	if len(more) != 0 {
		t.Fatalf("w1 must not claim a second slot of the same server, got %d", len(more))
	}
	// w2 takes the remaining slot.
	second, _ := st.ClaimJobs(ctx, "w2", model.PhaseFunnel, 10, nil)
	if len(second) != 1 {
		t.Fatalf("w2 claim = %d, want 1", len(second))
	}
	if first[0].JobID == second[0].JobID {
		t.Fatal("two workers claimed the same slot")
	}
}

func TestRequeueFailsExhausted(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	st.EnqueueJob(ctx, srvID, model.PhaseFunnel)

	// First claim: attempts becomes 1.
	if _, err := st.ClaimJobs(ctx, "w1", model.PhaseFunnel, 1, nil); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Lease expired and attempts (1) >= maxAttempts (1) -> failed, not requeued.
	requeued, failed, err := st.RequeueExpired(ctx, 0, 1)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != 0 || failed != 1 {
		t.Fatalf("requeued=%d failed=%d, want 0/1", requeued, failed)
	}
	n, _ := st.CountJobs(ctx, model.JobFailed)
	if n != 1 {
		t.Fatalf("failed jobs = %d, want 1", n)
	}
}

func TestOpenJobCountDrains(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	batch, _ := st.CreateBatch(ctx, "scheduled")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	st.EnqueueFanout(ctx, batch, srvID, model.PhaseFunnel, 1)

	open, _ := st.OpenJobCount(ctx, &batch)
	if open != 1 {
		t.Fatalf("open = %d, want 1", open)
	}
	recordPass(t, st, "w1", srvID, 20, 12.0)
	open, _ = st.OpenJobCount(ctx, &batch)
	if open != 0 {
		t.Fatalf("open after record = %d, want 0 (drained)", open)
	}
}

func TestApprovedServersCorroboration(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	mustWorker(t, st, "w2")
	batch, _ := st.CreateBatch(ctx, "scheduled")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	st.SetServerGeo(ctx, srvID, "FR", "FR1")
	st.EnqueueFanout(ctx, batch, srvID, model.PhaseFunnel, 2)

	// Two distinct workers both pass, with different speeds.
	recordPass(t, st, "w1", srvID, 30, 10.0)
	recordPass(t, st, "w2", srvID, 40, 20.0)

	// Requiring 2 distinct workers: approved, median of {10,20} = 15.
	got, err := st.ApprovedServers(ctx, &batch, 0, 1000, 2)
	if err != nil {
		t.Fatalf("approved: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("approved count = %d, want 1", len(got))
	}
	if got[0].Workers != 2 {
		t.Fatalf("workers = %d, want 2", got[0].Workers)
	}
	if got[0].MedianDlMbps != 15 {
		t.Fatalf("median = %v, want 15", got[0].MedianDlMbps)
	}
	if got[0].SeqName != "FR1" || got[0].Country != "FR" {
		t.Fatalf("geo not returned: %+v", got[0])
	}

	// Requiring 3 distinct workers: not approved (only 2 measured it).
	got, _ = st.ApprovedServers(ctx, &batch, 0, 1000, 3)
	if len(got) != 0 {
		t.Fatalf("required=3 approved %d, want 0", len(got))
	}

	// Raising the speed bar above both measurements drops it.
	got, _ = st.ApprovedServers(ctx, &batch, 1e6, 1000, 1)
	if len(got) != 0 {
		t.Fatalf("high speed bar approved %d, want 0", len(got))
	}
}

func mustWorker(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.UpsertWorker(context.Background(), model.Worker{
		ID:       id,
		Status:   "idle",
		Capacity: model.Capacity{Latency: 10, Speed: 2, BwMbps: 50},
	}); err != nil {
		t.Fatalf("upsert worker %s: %v", id, err)
	}
}
