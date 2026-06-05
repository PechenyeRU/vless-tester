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
}

// New creates an empty scheduler. onError is invoked when a job returns an
// error; pass nil to ignore errors.
func New(onError func(name string, err error)) *Scheduler {
	return &Scheduler{
		jobs:     map[string]*Job{},
		triggers: map[string]chan struct{}{},
		onError:  onError,
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
// stop when ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, job := range s.jobs {
		go s.runLoop(ctx, job, s.triggers[name])
	}
}

func (s *Scheduler) runLoop(ctx context.Context, job *Job, trigger <-chan struct{}) {
	if job.RunOnStart {
		s.exec(ctx, job)
	}
	// Re-arm a timer each iteration reading the current interval, so a dynamic
	// IntervalFn (settings-backed) takes effect without restarting the loop.
	for {
		var tick <-chan time.Time
		var timer *time.Timer
		if d := job.interval(); d > 0 {
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
			s.exec(ctx, job)
		case <-trigger:
			if timer != nil && !timer.Stop() {
				<-timer.C // drain the fired timer before re-arming
			}
			s.exec(ctx, job)
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
