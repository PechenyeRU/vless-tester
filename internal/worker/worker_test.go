package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// fakeCoord is a scriptable Coordinator capturing every call.
type fakeCoord struct {
	registerID string
	claimQueue [][]Job // each Claim pops the next batch
	reportErr  error

	registered bool
	reported   [][]Result
	nacked     [][]int64
	heartbeats []string
	claimCalls int
}

func (f *fakeCoord) Register(_ context.Context, id string, _ model.Capacity) (string, error) {
	f.registered = true
	if f.registerID != "" {
		return f.registerID, nil
	}
	return id, nil
}

func (f *fakeCoord) Heartbeat(_ context.Context, _, status string, _ model.Capacity) error {
	f.heartbeats = append(f.heartbeats, status)
	return nil
}

func (f *fakeCoord) Claim(_ context.Context, _ string, _ model.JobPhase, _ int) ([]Job, error) {
	f.claimCalls++
	if len(f.claimQueue) == 0 {
		return nil, nil
	}
	batch := f.claimQueue[0]
	f.claimQueue = f.claimQueue[1:]
	return batch, nil
}

func (f *fakeCoord) Report(_ context.Context, _ string, results []Result) (int, error) {
	if f.reportErr != nil {
		return 0, f.reportErr
	}
	f.reported = append(f.reported, results)
	return len(results), nil
}

func (f *fakeCoord) Nack(_ context.Context, _ string, ids []int64) error {
	f.nacked = append(f.nacked, ids)
	return nil
}

// echoRunner reports a fixed OK status, echoing the job id.
type echoRunner struct{ calls int }

func (r *echoRunner) Run(_ context.Context, job Job) Result {
	r.calls++
	lat := 10
	return Result{JobID: job.JobID, Status: string(model.StatusOK), LatencyMs: &lat}
}

func TestRunOnceProcessesAndReports(t *testing.T) {
	coord := &fakeCoord{claimQueue: [][]Job{{
		{JobID: 1, RawURI: "vless://a", Phase: "latency"},
		{JobID: 2, RawURI: "vless://b", Phase: "latency"},
	}}}
	run := &echoRunner{}
	w := &Worker{ID: "w1", Coord: coord, Runner: run}

	n, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 2 || run.calls != 2 {
		t.Fatalf("processed n=%d runner=%d, want 2/2", n, run.calls)
	}
	if len(coord.reported) != 1 || len(coord.reported[0]) != 2 {
		t.Fatalf("reported: %+v", coord.reported)
	}
	if coord.reported[0][0].JobID != 1 || coord.reported[0][1].JobID != 2 {
		t.Fatalf("job ids not echoed: %+v", coord.reported[0])
	}
	if len(coord.heartbeats) != 1 || coord.heartbeats[0] != "busy" {
		t.Fatalf("expected one busy heartbeat, got %+v", coord.heartbeats)
	}
}

func TestRunOnceEmptyClaim(t *testing.T) {
	coord := &fakeCoord{}
	w := &Worker{ID: "w1", Coord: coord, Runner: &echoRunner{}}
	n, err := w.RunOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("empty claim: n=%d err=%v", n, err)
	}
	if len(coord.reported) != 0 {
		t.Fatal("nothing should be reported on an empty claim")
	}
}

func TestRunOnceNacksOnReportFailure(t *testing.T) {
	coord := &fakeCoord{
		claimQueue: [][]Job{{{JobID: 1}, {JobID: 2}}},
		reportErr:  errors.New("boom"),
	}
	w := &Worker{ID: "w1", Coord: coord, Runner: &echoRunner{}}

	if _, err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("expected report error to propagate")
	}
	if len(coord.nacked) != 1 || len(coord.nacked[0]) != 2 {
		t.Fatalf("failed report should nack both jobs, got %+v", coord.nacked)
	}
}

func TestRunOnceNacksRestOnCancel(t *testing.T) {
	coord := &fakeCoord{claimQueue: [][]Job{{{JobID: 1}, {JobID: 2}, {JobID: 3}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled: the loop nacks the whole batch before running any
	w := &Worker{ID: "w1", Coord: coord, Runner: &echoRunner{}}

	_, _ = w.RunOnce(ctx)
	if len(coord.nacked) != 1 || len(coord.nacked[0]) != 3 {
		t.Fatalf("canceled batch should nack all 3, got %+v", coord.nacked)
	}
}

func TestRunRegistersAndAdoptsID(t *testing.T) {
	coord := &fakeCoord{registerID: "assigned-name-1"} // empty queue: stays idle
	// Bound the loop with a short deadline; a tiny idle keeps the test fast.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w := &Worker{ID: "", Coord: coord, Runner: &echoRunner{}, Idle: time.Millisecond}

	_ = w.Run(ctx)

	if !coord.registered {
		t.Fatal("worker did not register")
	}
	if w.ID != "assigned-name-1" {
		t.Fatalf("worker did not adopt coordinator id, got %q", w.ID)
	}
	if len(coord.heartbeats) == 0 || coord.heartbeats[0] != "idle" {
		t.Fatalf("expected idle heartbeats while queue empty, got %+v", coord.heartbeats)
	}
}
