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

// Worker runs a claim -> test -> report pipeline. Probes flow through a fixed
// pool of testers continuously: a dead server (which fails its latency probe in
// a few seconds) frees its slot immediately instead of waiting on the batch's
// slow speed tests, so one slow probe no longer stalls the rest. This is the
// throughput win over a claim-whole-batch / wait-for-all / report model.
type Worker struct {
	ID       string
	Capacity model.Capacity
	Coord    Coordinator
	Runner   Runner
	// BatchMax is the most jobs to claim per round; 0 lets the coordinator pick.
	BatchMax int
	// Concurrency caps how many probes run at once. Latency probes are cheap so
	// this is high; the speed leg is throttled separately inside the runner.
	// 0 defaults to Capacity.Latency, then to the batch size.
	Concurrency int
	// Idle is how long to wait before polling again when the queue is empty.
	Idle time.Duration
	Logf func(format string, args ...any)
}

// reportFlushMax caps how many results are sent in one Report call; bursts under
// load are batched up to this, and the buffer is flushed as soon as no more
// results are immediately available.
const reportFlushMax = 100

// heartbeatEvery throttles "busy" heartbeats while the pipeline is working, so a
// fast claim loop does not call the coordinator on every round.
const heartbeatEvery = 15 * time.Second

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

// poolSize is the steady-state tester-pool size (independent of any single
// claim's length). It prefers Concurrency, then Capacity.Latency, then BatchMax.
func (w *Worker) poolSize() int {
	if w.Concurrency > 0 {
		return w.Concurrency
	}
	if w.Capacity.Latency > 0 {
		return w.Capacity.Latency
	}
	if w.BatchMax > 0 {
		return w.BatchMax
	}
	return 1
}

func (w *Worker) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	}
}

// Run registers the worker, then runs the claim/test/report pipeline until ctx
// is canceled. Claimed-but-unprocessed jobs are released on shutdown so they
// retry elsewhere instead of waiting out the lease.
func (w *Worker) Run(ctx context.Context) error {
	id, err := w.Coord.Register(ctx, w.ID, w.Capacity)
	if err != nil {
		return err
	}
	w.ID = id
	w.logf("worker %s registered (cap %+v)", w.ID, w.Capacity)

	pool := w.poolSize()
	jobsCh := make(chan Job, pool)
	resultsCh := make(chan Result, pool)

	// Tester pool: each goroutine pulls a job, runs the funnel, emits a result.
	var testers sync.WaitGroup
	for i := 0; i < pool; i++ {
		testers.Add(1)
		go func() {
			defer testers.Done()
			for job := range jobsCh {
				res := w.Runner.Run(ctx, job)
				res.JobID = job.JobID
				resultsCh <- res
			}
		}()
	}

	// Reporter: batches results and reports them; nacks any it cannot report.
	var reporter sync.WaitGroup
	reporter.Add(1)
	go func() {
		defer reporter.Done()
		w.reportLoop(resultsCh)
	}()

	// Dispatcher (this goroutine): claims continuously and feeds the pool.
	w.dispatchLoop(ctx, jobsCh)

	// Shutdown: stop feeding, let in-flight probes drain and report, then stop.
	close(jobsCh)
	testers.Wait()
	close(resultsCh)
	reporter.Wait()
	return ctx.Err()
}

// dispatchLoop claims work and pushes it into jobsCh, applying backpressure via
// the channel buffer so the worker never leases far more than it can process. It
// closes nothing (the caller closes jobsCh) and returns when ctx is canceled.
func (w *Worker) dispatchLoop(ctx context.Context, jobsCh chan<- Job) {
	idle := w.Idle
	if idle <= 0 {
		idle = 5 * time.Second
	}
	var lastBusy time.Time

	for {
		if ctx.Err() != nil {
			return
		}
		batch, err := w.Coord.Claim(ctx, w.ID, "", w.BatchMax)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logf("worker %s claim error: %v", w.ID, err)
			if !sleep(ctx, idle) {
				return
			}
			continue
		}
		if len(batch) == 0 {
			_ = w.Coord.Heartbeat(ctx, w.ID, "idle", w.Capacity)
			if !sleep(ctx, idle) {
				return
			}
			continue
		}
		if time.Since(lastBusy) >= heartbeatEvery {
			_ = w.Coord.Heartbeat(ctx, w.ID, "busy", w.Capacity)
			lastBusy = time.Now()
		}
		for i, job := range batch {
			select {
			case jobsCh <- job:
			case <-ctx.Done():
				// Release the jobs we claimed but never dispatched.
				w.nackRest(batch[i:])
				return
			}
		}
	}
}

// reportLoop drains results, reporting them in batches. It blocks for the first
// result then sweeps up everything immediately available (up to reportFlushMax),
// so it batches bursts under load yet reports promptly when the pool is quiet.
func (w *Worker) reportLoop(resultsCh <-chan Result) {
	for {
		first, ok := <-resultsCh
		if !ok {
			return
		}
		buf := []Result{first}
	drain:
		for len(buf) < reportFlushMax {
			select {
			case r, ok := <-resultsCh:
				if !ok {
					w.flush(buf)
					return
				}
				buf = append(buf, r)
			default:
				break drain
			}
		}
		w.flush(buf)
	}
}

// flush reports a batch of results, releasing them back to the queue if the
// report fails so another worker retries them instead of losing the work.
func (w *Worker) flush(results []Result) {
	if len(results) == 0 {
		return
	}
	// A fresh context: results must be reported (or nacked) even as the worker
	// shuts down, so a canceled parent does not abandon completed work.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.Coord.Report(ctx, w.ID, results); err != nil {
		w.logf("worker %s report failed (%d results): %v", w.ID, len(results), err)
		ids := make([]int64, len(results))
		for i, r := range results {
			ids[i] = r.JobID
		}
		w.nackIDs(ids)
	}
}

func (w *Worker) nackRest(jobs []Job) {
	if len(jobs) == 0 {
		return
	}
	ids := make([]int64, len(jobs))
	for i, j := range jobs {
		ids[i] = j.JobID
	}
	w.nackIDs(ids)
}

func (w *Worker) nackIDs(ids []int64) {
	if len(ids) == 0 {
		return
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
