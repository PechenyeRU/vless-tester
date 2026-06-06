package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// fakeCoord is a scriptable, concurrency-safe Coordinator capturing every call.
// The pipeline reports and nacks from different goroutines, so access is locked.
type fakeCoord struct {
	registerID string

	mu         sync.Mutex
	claimQueue [][]Job // each Claim pops the next batch
	reportErr  error
	registered bool
	reported   [][]Result
	nacked     [][]int64
	heartbeats []string
	claimCalls int
}

func (f *fakeCoord) Register(_ context.Context, id string, _ model.Capacity) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = true
	if f.registerID != "" {
		return f.registerID, nil
	}
	return id, nil
}

func (f *fakeCoord) Heartbeat(_ context.Context, _, status string, _ model.Capacity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats = append(f.heartbeats, status)
	return nil
}

func (f *fakeCoord) Claim(_ context.Context, _ string, _ model.JobPhase, _ int) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls++
	if len(f.claimQueue) == 0 {
		return nil, nil
	}
	batch := f.claimQueue[0]
	f.claimQueue = f.claimQueue[1:]
	return batch, nil
}

func (f *fakeCoord) Report(_ context.Context, _ string, results []Result) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reportErr != nil {
		return 0, f.reportErr
	}
	cp := append([]Result(nil), results...)
	f.reported = append(f.reported, cp)
	return len(cp), nil
}

func (f *fakeCoord) Nack(_ context.Context, _ string, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nacked = append(f.nacked, append([]int64(nil), ids...))
	return nil
}

// reportedIDs returns the flattened set of reported job ids.
func (f *fakeCoord) reportedIDs() map[int64]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]bool{}
	for _, batch := range f.reported {
		for _, r := range batch {
			out[r.JobID] = true
		}
	}
	return out
}

// nackedIDs returns the flattened set of nacked job ids.
func (f *fakeCoord) nackedIDs() map[int64]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int64]bool{}
	for _, batch := range f.nacked {
		for _, id := range batch {
			out[id] = true
		}
	}
	return out
}

func (f *fakeCoord) statuses() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.heartbeats...)
}

// echoRunner reports a fixed OK status, echoing the job id.
type echoRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *echoRunner) Run(_ context.Context, job Job) Result {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	lat := 10
	return Result{JobID: job.JobID, Status: string(model.StatusOK), LatencyMs: &lat}
}

func (r *echoRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// runFor drives Run on a bounded context so the pipeline starts, drains the
// scripted queue, and stops deterministically.
func runFor(w *Worker, d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = w.Run(ctx)
}

func TestRunProcessesAndReports(t *testing.T) {
	coord := &fakeCoord{claimQueue: [][]Job{{
		{JobID: 1, RawURI: "vless://a", Phase: "latency"},
		{JobID: 2, RawURI: "vless://b", Phase: "latency"},
	}}}
	run := &echoRunner{}
	w := &Worker{ID: "w1", Coord: coord, Runner: run, Concurrency: 4, Idle: time.Millisecond}

	runFor(w, 300*time.Millisecond)

	if run.count() != 2 {
		t.Fatalf("runner calls = %d, want 2", run.count())
	}
	got := coord.reportedIDs()
	if !got[1] || !got[2] || len(got) != 2 {
		t.Fatalf("reported ids = %v, want {1,2}", got)
	}
}

func TestRunEmptyClaimGoesIdle(t *testing.T) {
	coord := &fakeCoord{}
	w := &Worker{ID: "w1", Coord: coord, Runner: &echoRunner{}, Idle: time.Millisecond}

	runFor(w, 50*time.Millisecond)

	if len(coord.reportedIDs()) != 0 {
		t.Fatal("nothing should be reported on an empty claim")
	}
	st := coord.statuses()
	if len(st) == 0 || st[0] != "idle" {
		t.Fatalf("expected idle heartbeats while queue empty, got %+v", st)
	}
}

func TestRunNacksOnReportFailure(t *testing.T) {
	coord := &fakeCoord{
		claimQueue: [][]Job{{{JobID: 1}, {JobID: 2}}},
		reportErr:  errors.New("boom"),
	}
	w := &Worker{ID: "w1", Coord: coord, Runner: &echoRunner{}, Concurrency: 4, Idle: time.Millisecond}

	runFor(w, 300*time.Millisecond)

	// A failed report must release the work so another worker retries it.
	nacked := coord.nackedIDs()
	if !nacked[1] || !nacked[2] {
		t.Fatalf("failed report should nack both jobs, got %v", nacked)
	}
}

// slowRunner sleeps so jobs are still in flight / queued when the context is
// canceled mid-pipeline, exercising the shutdown path.
type slowRunner struct{ d time.Duration }

func (r slowRunner) Run(_ context.Context, job Job) Result {
	time.Sleep(r.d)
	return Result{JobID: job.JobID, Status: string(model.StatusOK)}
}

func TestRunNoJobLostOnCancel(t *testing.T) {
	coord := &fakeCoord{claimQueue: [][]Job{{{JobID: 1}, {JobID: 2}, {JobID: 3}}}}
	// Pool of 1 with a slow runner forces backpressure: one job in flight, one
	// buffered, one stuck on the dispatcher's send when the cancel lands.
	w := &Worker{ID: "w1", Coord: coord, Runner: slowRunner{d: 100 * time.Millisecond}, Concurrency: 1, Idle: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_ = w.Run(ctx)

	// Every claimed job must end up either reported or nacked — never dropped.
	covered := coord.reportedIDs()
	for id := range coord.nackedIDs() {
		covered[id] = true
	}
	for _, id := range []int64{1, 2, 3} {
		if !covered[id] {
			t.Fatalf("job %d was neither reported nor nacked: reported=%v nacked=%v", id, coord.reportedIDs(), coord.nackedIDs())
		}
	}
}

func TestRunRegistersAndAdoptsID(t *testing.T) {
	coord := &fakeCoord{registerID: "assigned-name-1"} // empty queue: stays idle
	w := &Worker{ID: "", Coord: coord, Runner: &echoRunner{}, Idle: time.Millisecond}

	runFor(w, 40*time.Millisecond)

	coord.mu.Lock()
	registered := coord.registered
	coord.mu.Unlock()
	if !registered {
		t.Fatal("worker did not register")
	}
	if w.ID != "assigned-name-1" {
		t.Fatalf("worker did not adopt coordinator id, got %q", w.ID)
	}
}
