package worker

import (
	"context"
	"sync"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// Coordinator is the control plane the worker pulls from. *Client is the
// production implementation; tests inject a fake.
type Coordinator interface {
	Register(ctx context.Context, id string, capacity model.Capacity) (string, error)
	Heartbeat(ctx context.Context, id, status string, free model.Capacity) error
	Claim(ctx context.Context, id string, phase model.JobPhase, limit int) ([]Job, error)
	Report(ctx context.Context, id string, results []Result) (int, error)
	Nack(ctx context.Context, id string, jobIDs []int64) error
}

// Runner executes a single job and always returns a Result. A failure to even
// start the proxy is reported as an error-status Result rather than dropped, so
// every claimed job is accounted for.
type Runner interface {
	Run(ctx context.Context, job Job) Result
}

// Worker runs the claim -> test -> report loop.
type Worker struct {
	ID       string
	Capacity model.Capacity
	Coord    Coordinator
	Runner   Runner
	// BatchMax is the most jobs to claim per cycle; 0 lets the coordinator pick.
	BatchMax int
	// Concurrency caps how many jobs run at once. Latency probes are cheap so
	// this is high; the speed leg is throttled separately inside the runner.
	// 0 defaults to Capacity.Latency, then to the batch size.
	Concurrency int
	// Idle is how long to wait before polling again when the queue is empty.
	Idle time.Duration
	Logf func(format string, args ...any)
}

// concurrency resolves the per-cycle worker-pool size for a batch of n jobs.
func (w *Worker) concurrency(n int) int {
	c := w.Concurrency
	if c <= 0 {
		c = w.Capacity.Latency
	}
	if c <= 0 || c > n {
		c = n
	}
	return c
}

func (w *Worker) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	}
}

// Run registers the worker, then loops until ctx is canceled.
func (w *Worker) Run(ctx context.Context) error {
	id, err := w.Coord.Register(ctx, w.ID, w.Capacity)
	if err != nil {
		return err
	}
	w.ID = id
	w.logf("worker %s registered (cap %+v)", w.ID, w.Capacity)

	idle := w.Idle
	if idle <= 0 {
		idle = 5 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := w.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.logf("worker %s cycle error: %v", w.ID, err)
		}
		if n == 0 {
			// Nothing to do: report idle liveness and back off.
			_ = w.Coord.Heartbeat(ctx, w.ID, "idle", w.Capacity)
			if !sleep(ctx, idle) {
				return ctx.Err()
			}
		}
	}
}

// RunOnce performs one claim/process/report cycle and returns the number of
// jobs processed. It is the unit the loop drives, exposed so tests can step the
// worker deterministically.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	jobs, err := w.Coord.Claim(ctx, w.ID, "", w.BatchMax)
	if err != nil {
		return 0, err
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	_ = w.Coord.Heartbeat(ctx, w.ID, "busy", w.Capacity)

	// Run the batch concurrently, preserving order via an indexed slice. A job
	// not run (because ctx was canceled before its turn) is left nil and nacked.
	slots := make([]*Result, len(jobs))
	sem := make(chan struct{}, w.concurrency(len(jobs)))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			res := w.Runner.Run(ctx, job)
			res.JobID = job.JobID
			slots[i] = &res
		}(i, job)
	}
	wg.Wait()

	results := make([]Result, 0, len(jobs))
	doneJobs := make([]Job, 0, len(jobs))
	var undone []Job
	for i, job := range jobs {
		if slots[i] != nil {
			results = append(results, *slots[i])
			doneJobs = append(doneJobs, job)
		} else {
			undone = append(undone, job)
		}
	}

	// Release jobs we never ran (mid-batch cancellation) so they retry elsewhere
	// instead of waiting out the lease.
	w.nackRest(undone)

	if len(results) > 0 {
		if _, err := w.Coord.Report(ctx, w.ID, results); err != nil {
			// Report failed: release the tested jobs so another worker retries.
			w.nackRest(doneJobs)
			return 0, err
		}
	}
	return len(results), nil
}

func (w *Worker) nackRest(jobs []Job) {
	if len(jobs) == 0 {
		return
	}
	ids := make([]int64, len(jobs))
	for i, j := range jobs {
		ids[i] = j.JobID
	}
	// Use a fresh context so a canceled parent does not also abort the release.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Coord.Nack(ctx, w.ID, ids); err != nil {
		w.logf("worker %s nack failed: %v", w.ID, err)
	}
}

// sleep waits for d or ctx cancellation; it returns false if ctx was canceled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
