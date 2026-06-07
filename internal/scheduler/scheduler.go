// Package scheduler runs the coordinator's periodic jobs (source refresh,
// retest, publish, geoip refresh) and supports manual out-of-band triggers. It
// is the single source of scheduling: there are no external cron jobs. See
// DESIGN.md.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Store persists per-job last-run times so a job's cadence survives process
// restarts. Optional: without a Store, every RunOnStart job runs on each boot.
type Store interface {
	LastRun(ctx context.Context, name string) (time.Time, bool, error)
	SetLastRun(ctx context.Context, name string, t time.Time) error
}

// Job is a named periodic task.
type Job struct {
	Name string
	// Interval is the fixed run period. IntervalFn, when set, takes precedence and
	// is re-read after every run, so a settings-driven interval applies live (on
	// the next cycle) without restarting the scheduler.
	Interval   time.Duration
	IntervalFn func() time.Duration
	Run        func(ctx context.Context) error
	RunOnStart bool // execute once immediately when the scheduler starts
	// Persistent ties this job's cadence to the Store: its last run is recorded,
	// and on boot it runs (and re-arms its timer) from that record rather than
	// firing on every restart. Use for expensive jobs (e.g. dispatch) that must
	// not re-trigger each redeploy. Requires a Store; otherwise behaves normally.
	Persistent bool
}

// interval returns the job's current run period (dynamic when IntervalFn is set).
func (j *Job) interval() time.Duration {
	if j.IntervalFn != nil {
		return j.IntervalFn()
	}
	return j.Interval
}

// Scheduler runs jobs on their intervals and on manual triggers.
type Scheduler struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	triggers map[string]chan struct{}
	onError  func(name string, err error)
	store    Store            // optional, for Persistent jobs
	now      func() time.Time // injectable clock (tests); defaults to time.Now
}

// SetStore enables persistence for Persistent jobs. Call before Start.
func (s *Scheduler) SetStore(store Store) { s.store = store }

// New creates an empty scheduler. onError is invoked when a job returns an
// error; pass nil to ignore errors.
func New(onError func(name string, err error)) *Scheduler {
	return &Scheduler{
		jobs:     map[string]*Job{},
		triggers: map[string]chan struct{}{},
		onError:  onError,
		now:      time.Now,
	}
}

// Add registers a job. Adding a job with an existing name replaces it.
func (s *Scheduler) Add(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := job
	s.jobs[job.Name] = &j
	s.triggers[job.Name] = make(chan struct{}, 1)
}

// Trigger requests an out-of-band run of a job. It is non-blocking: if a trigger
// is already pending, the call is a no-op.
func (s *Scheduler) Trigger(name string) error {
	s.mu.Lock()
	ch, ok := s.triggers[name]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("scheduler: unknown job %q", name)
	}
	select {
	case ch <- struct{}{}:
	default:
	}
	return nil
}

// Start launches every job in its own goroutine and returns immediately. Jobs
// stop when ctx is canceled.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, job := range s.jobs {
		go s.runLoop(ctx, job, s.triggers[name])
	}
}

func (s *Scheduler) runLoop(ctx context.Context, job *Job, trigger <-chan struct{}) {
	if job.RunOnStart && s.dueOnStart(ctx, job) {
		s.run(ctx, job)
	}
	// Re-arm a timer each iteration reading the current interval, so a dynamic
	// IntervalFn (settings-backed) takes effect without restarting the loop. For a
	// Persistent job the first wait is shortened by however long ago it last ran,
	// so its cadence is unaffected by restarts.
	for {
		var tick <-chan time.Time
		var timer *time.Timer
		if d := s.nextDelay(ctx, job); d >= 0 {
			timer = time.NewTimer(d)
			tick = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-tick:
			s.run(ctx, job)
		case <-trigger:
			if timer != nil && !timer.Stop() {
				<-timer.C // drain the fired timer before re-arming
			}
			s.run(ctx, job) // a manual trigger always runs, schedule aside
		}
	}
}

// dueOnStart reports whether a RunOnStart job should fire on boot. Non-persistent
// jobs always do; a persistent one runs only if its interval has elapsed since
// the recorded last run (so a redeploy does not re-trigger it).
func (s *Scheduler) dueOnStart(ctx context.Context, job *Job) bool {
	if !job.Persistent || s.store == nil {
		return true
	}
	last, ok, err := s.store.LastRun(ctx, job.Name)
	if err != nil || !ok {
		return true
	}
	d := job.interval()
	return d <= 0 || s.now().Sub(last) >= d
}

// nextDelay is how long to wait before the next scheduled run. It returns -1 to
// mean "no timer" (manual-only job, interval <= 0). For a persistent job it
// subtracts the elapsed time since the last recorded run, clamped to zero.
func (s *Scheduler) nextDelay(ctx context.Context, job *Job) time.Duration {
	d := job.interval()
	if d <= 0 {
		return -1
	}
	if !job.Persistent || s.store == nil {
		return d
	}
	last, ok, err := s.store.LastRun(ctx, job.Name)
	if err != nil || !ok {
		return d
	}
	if rem := d - s.now().Sub(last); rem > 0 {
		return rem
	}
	return 0
}

// run executes a job and, for a persistent job, records the run time so the
// schedule survives restarts.
func (s *Scheduler) run(ctx context.Context, job *Job) {
	s.exec(ctx, job)
	if job.Persistent && s.store != nil {
		if err := s.store.SetLastRun(ctx, job.Name, s.now()); err != nil && s.onError != nil {
			s.onError(job.Name, fmt.Errorf("record last run: %w", err))
		}
	}
}

// exec runs a job, recovering from panics and reporting errors.
func (s *Scheduler) exec(ctx context.Context, job *Job) {
	defer func() {
		if r := recover(); r != nil && s.onError != nil {
			s.onError(job.Name, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := job.Run(ctx); err != nil && s.onError != nil {
		s.onError(job.Name, err)
	}
}

// ParseInterval parses a duration string (e.g. "12h"), returning def on failure.
func ParseInterval(s string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
