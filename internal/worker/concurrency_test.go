package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/model"
)

// trackRunner records peak concurrency for the (ungated) latency portion and the
// gated speed portion, mirroring how ProbeRunner runs latency wide and speed
// behind a shared semaphore.
type trackRunner struct {
	gate           *checks.Semaphore
	delay          time.Duration
	latCur, latMax int32
	spdCur, spdMax int32
}

func (r *trackRunner) Run(ctx context.Context, job Job) Result {
	bump(&r.latCur, &r.latMax)
	time.Sleep(r.delay)
	atomic.AddInt32(&r.latCur, -1)

	if r.gate != nil {
		if err := r.gate.Acquire(ctx); err == nil {
			bump(&r.spdCur, &r.spdMax)
			time.Sleep(r.delay)
			atomic.AddInt32(&r.spdCur, -1)
			r.gate.Release()
		}
	}
	return Result{JobID: job.JobID, Status: string(model.StatusOK)}
}

func bump(cur, limit *int32) {
	n := atomic.AddInt32(cur, 1)
	for {
		m := atomic.LoadInt32(limit)
		if n <= m || atomic.CompareAndSwapInt32(limit, m, n) {
			return
		}
	}
}

func TestRunOnceConcurrentWithGatedSpeed(t *testing.T) {
	const jobs = 8
	batch := make([]Job, jobs)
	for i := range batch {
		batch[i] = Job{JobID: int64(i + 1), Phase: "funnel"}
	}
	coord := &fakeCoord{claimQueue: [][]Job{batch}}
	run := &trackRunner{gate: checks.NewSemaphore(2), delay: 10 * time.Millisecond}
	w := &Worker{ID: "w1", Coord: coord, Runner: run, Concurrency: jobs}

	n, err := w.RunOnce(context.Background())
	if err != nil || n != jobs {
		t.Fatalf("RunOnce n=%d err=%v, want %d", n, err, jobs)
	}
	if len(coord.reported) != 1 || len(coord.reported[0]) != jobs {
		t.Fatalf("reported %+v, want %d results", coord.reported, jobs)
	}

	// Latency ran wide (more than the speed gate would allow)...
	if atomic.LoadInt32(&run.latMax) <= 2 {
		t.Fatalf("latency peak concurrency = %d, expected > 2 (wide fan-out)", run.latMax)
	}
	// ...while the speed leg never exceeded the gate size.
	if got := atomic.LoadInt32(&run.spdMax); got > 2 {
		t.Fatalf("speed peak concurrency = %d, want <= 2 (gated)", got)
	}
}

func TestConcurrencyDefaultsToCapacityLatency(t *testing.T) {
	w := &Worker{Capacity: model.Capacity{Latency: 50}}
	if got := w.concurrency(200); got != 50 {
		t.Fatalf("concurrency = %d, want 50 (capacity.Latency)", got)
	}
	if got := w.concurrency(10); got != 10 {
		t.Fatalf("concurrency = %d, want 10 (capped to batch size)", got)
	}
	// No capacity and no override: fall back to the batch size.
	w2 := &Worker{}
	if got := w2.concurrency(7); got != 7 {
		t.Fatalf("concurrency = %d, want 7 (batch size fallback)", got)
	}
}
