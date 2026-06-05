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
		`TRUNCATE checks, test_runs, jobs, servers, workers, sources RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate: %v", err)
	}
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
	claimed, err := st.ClaimJobs(ctx, "w1", model.PhaseLatency, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim got %d err=%v", len(claimed), err)
	}
	// ttl=0 requeues anything claimed in the past.
	n, err := st.RequeueExpired(ctx, 0)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 {
		t.Fatalf("requeued %d, want 1", n)
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
				jobs, err := st.ClaimJobs(ctx, worker, model.PhaseLatency, 4)
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
