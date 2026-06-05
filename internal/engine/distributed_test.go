package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/engine"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
	"github.com/whitedns/vless-tester/internal/output"
	"github.com/whitedns/vless-tester/internal/store"
)

// newDistributedEngine builds an engine for the dispatch/reconcile path. No
// prober or checks are wired: remote workers (here simulated against the store)
// do the testing; the engine only dispatches, reconciles, and publishes.
func newDistributedEngine(st *store.Store, approval engine.Approval, fanout int) *engine.Engine {
	return &engine.Engine{
		Store:       st,
		Resolver:    fakeResolver{"8.8.8.8": "FR", "1.1.1.1": "DE"},
		Seq:         naming.Allocator{Backend: st.NewSeqBackend()},
		Publisher:   &output.MockPublisher{},
		Brand:       "@WhiteDNS",
		Approval:    approval,
		Fanout:      fanout,
		AliveWindow: time.Hour, // workers registered in-test always count as alive
	}
}

// workerPass claims every funnel job available to a worker and reports each as a
// pass, standing in for a real remote probe.
func workerPass(t *testing.T, st *store.Store, worker string, latency int, dl float64) int {
	t.Helper()
	ctx := context.Background()
	claimed, err := st.ClaimJobs(ctx, worker, model.PhaseFunnel, 100)
	if err != nil {
		t.Fatalf("claim for %s: %v", worker, err)
	}
	for _, j := range claimed {
		ok, err := st.RecordResult(ctx, worker, j.JobID, model.TestRun{
			Status: model.StatusOK, LatencyMs: &latency, DlMbps: &dl,
		})
		if err != nil || !ok {
			t.Fatalf("record %s job %d: ok=%v err=%v", worker, j.JobID, ok, err)
		}
	}
	return len(claimed)
}

func twoServers(t *testing.T) []model.Server {
	t.Helper()
	servers, _ := ingest.ParseList("vless://uuid@8.8.8.8:443?type=ws#a\ntrojan://pw@1.1.1.1:443#b")
	if len(servers) != 2 {
		t.Fatalf("parsed %d servers, want 2", len(servers))
	}
	return servers
}

func TestDispatchCycleFanoutAndGuard(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorkerE(t, st, "w1")
	mustWorkerE(t, st, "w2")

	eng := newDistributedEngine(st, engine.Approval{MaxLatencyMs: 60000, RequiredWorkers: 2}, 2)
	batch, dispatched, err := eng.DispatchCycle(ctx, twoServers(t))
	if err != nil || !dispatched {
		t.Fatalf("dispatch: dispatched=%v err=%v", dispatched, err)
	}
	// 2 servers x fanout 2 = 4 queued jobs.
	queued, _ := st.CountJobs(ctx, model.JobQueued)
	if queued != 4 {
		t.Fatalf("queued = %d, want 4", queued)
	}
	if batch == 0 {
		t.Fatal("expected a batch id")
	}

	// A second dispatch is refused while the batch is still active.
	_, again, err := eng.DispatchCycle(ctx, twoServers(t))
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if again {
		t.Fatal("dispatch must be refused while a cycle is in progress")
	}
}

func TestDistributedCycleEndToEnd(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorkerE(t, st, "w1")
	mustWorkerE(t, st, "w2")

	eng := newDistributedEngine(st, engine.Approval{MaxLatencyMs: 60000, RequiredWorkers: 2}, 2)
	if _, _, err := eng.DispatchCycle(ctx, twoServers(t)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Both workers test both servers (distinct-worker claim spreads the slots).
	if n := workerPass(t, st, "w1", 30, 10); n != 2 {
		t.Fatalf("w1 claimed %d, want 2", n)
	}
	if n := workerPass(t, st, "w2", 40, 20); n != 2 {
		t.Fatalf("w2 claimed %d, want 2", n)
	}

	// Batch is drained: reconcile finishes it and publishes.
	res, err := eng.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !res.Published {
		t.Fatalf("expected publish on drained batch, got %+v", res)
	}
	if res.Approved != 2 {
		t.Fatalf("approved = %d, want 2", res.Approved)
	}

	pub := eng.Publisher.(*output.MockPublisher)
	if pub.Calls != 1 {
		t.Fatalf("publisher calls = %d, want 1", pub.Calls)
	}
	// Names were assigned at publish time from GeoIP.
	s1, _ := st.GetServer(ctx, 1)
	if s1.Country == "" || s1.SeqName == "" {
		t.Fatalf("server 1 not named: %+v", s1)
	}
}

func TestReconcileRequeuesDeadWorker(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorkerE(t, st, "w1")
	mustWorkerE(t, st, "w2")

	// A sub-millisecond lease makes any claim immediately eligible for requeue;
	// a high attempt cap means it is requeued, not failed.
	eng := newDistributedEngine(st, engine.Approval{MaxLatencyMs: 60000, RequiredWorkers: 1}, 1)
	eng.LeaseTTL = time.Nanosecond
	eng.MaxAttempts = 5
	if _, _, err := eng.DispatchCycle(ctx, twoServers(t)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// w1 claims but dies without reporting.
	dead, _ := st.ClaimJobs(ctx, "w1", model.PhaseFunnel, 100)
	if len(dead) == 0 {
		t.Fatal("w1 claimed nothing")
	}
	res, err := eng.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Requeued != int64(len(dead)) {
		t.Fatalf("requeued = %d, want %d", res.Requeued, len(dead))
	}
	if res.Published {
		t.Fatal("must not publish while jobs are still open")
	}

	// A live worker (w2) now drains everything and the cycle completes.
	workerPass(t, st, "w2", 25, 12)
	res, err = eng.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if !res.Published {
		t.Fatalf("expected publish after drain, got %+v", res)
	}
}

// TestAllowPartialVsStrict shows the small-fleet policy: with one worker and
// N=2, allow_partial publishes from the single proof, while strict approves
// nothing.
func TestAllowPartialVsStrict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorkerE(t, st, "w1") // a fleet of one

	eng := newDistributedEngine(st, engine.Approval{MaxLatencyMs: 60000, RequiredWorkers: 2, AllowPartial: true}, 2)
	if _, _, err := eng.DispatchCycle(ctx, twoServers(t)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Fan-out was capped to the lone worker: one slot per server.
	queued, _ := st.CountJobs(ctx, model.JobQueued)
	if queued != 2 {
		t.Fatalf("queued = %d, want 2 (fanout capped to 1 worker)", queued)
	}

	workerPass(t, st, "w1", 20, 15)
	res, err := eng.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !res.Published || res.Approved != 2 {
		t.Fatalf("allow_partial: published=%v approved=%d, want true/2", res.Published, res.Approved)
	}

	// Same history, strict gate (no partial): one proof < required 2 -> nothing.
	eng.Approval = engine.Approval{MaxLatencyMs: 60000, RequiredWorkers: 2, AllowPartial: false}
	strict, err := eng.PublishFromHistory(ctx)
	if err != nil {
		t.Fatalf("strict publish: %v", err)
	}
	if strict.Approved != 0 {
		t.Fatalf("strict approved = %d, want 0", strict.Approved)
	}
}

func mustWorkerE(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.UpsertWorker(context.Background(), model.Worker{ID: id, Status: "idle"}); err != nil {
		t.Fatalf("worker %s: %v", id, err)
	}
}
