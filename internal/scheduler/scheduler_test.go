package scheduler_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/scheduler"
)

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in   string
		def  time.Duration
		want time.Duration
	}{
		{"12h", time.Minute, 12 * time.Hour},
		{"30s", time.Minute, 30 * time.Second},
		{"", time.Minute, time.Minute},        // empty -> default
		{"garbage", time.Minute, time.Minute}, // invalid -> default
		{"-5s", time.Minute, time.Minute},     // non-positive -> default
	}
	for _, c := range cases {
		if got := scheduler.ParseInterval(c.in, c.def); got != c.want {
			t.Errorf("ParseInterval(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRunsOnStart(t *testing.T) {
	ran := make(chan struct{}, 1)
	s := scheduler.New(nil)
	s.Add(scheduler.Job{
		Name:       "boot",
		RunOnStart: true,
		Run: func(context.Context) error {
			select {
			case ran <- struct{}{}:
			default:
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("RunOnStart job did not execute")
	}
}

func TestManualTrigger(t *testing.T) {
	ran := make(chan struct{}, 4)
	s := scheduler.New(nil)
	s.Add(scheduler.Job{
		Name:     "manual",
		Interval: time.Hour, // long; only the trigger should fire it
		Run: func(context.Context) error {
			ran <- struct{}{}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	if err := s.Trigger("manual"); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("manual trigger did not execute the job")
	}
}

func TestTriggerUnknownJob(t *testing.T) {
	s := scheduler.New(nil)
	if err := s.Trigger("nope"); err == nil {
		t.Fatal("expected error triggering unknown job")
	}
}

func TestPeriodicExecution(t *testing.T) {
	var count atomic.Int32
	done := make(chan struct{})
	s := scheduler.New(nil)
	s.Add(scheduler.Job{
		Name:     "tick",
		Interval: 10 * time.Millisecond,
		Run: func(context.Context) error {
			if count.Add(1) == 3 {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected >=3 periodic executions, got %d", count.Load())
	}
}

func TestDynamicInterval(t *testing.T) {
	var count atomic.Int32
	done := make(chan struct{})
	s := scheduler.New(nil)
	// IntervalFn is re-read each cycle; a short dynamic interval ticks repeatedly.
	s.Add(scheduler.Job{
		Name:       "dyn",
		IntervalFn: func() time.Duration { return 10 * time.Millisecond },
		Run: func(context.Context) error {
			if count.Add(1) == 3 {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected >=3 dynamic-interval executions, got %d", count.Load())
	}
}

func TestErrorCallback(t *testing.T) {
	got := make(chan string, 1)
	s := scheduler.New(func(name string, _ error) {
		select {
		case got <- name:
		default:
		}
	})
	s.Add(scheduler.Job{
		Name:       "failing",
		RunOnStart: true,
		Run:        func(context.Context) error { panic("boom") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case name := <-got:
		if name != "failing" {
			t.Fatalf("error callback name = %q", name)
		}
	case <-time.After(time.Second):
		t.Fatal("panic was not reported to onError")
	}
}

// fakeStore is an in-memory scheduler.Store for the persistence tests.
type fakeStore struct {
	mu   sync.Mutex
	last map[string]time.Time
	sets int32
}

func (f *fakeStore) LastRun(_ context.Context, name string) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.last[name]
	return t, ok, nil
}

func (f *fakeStore) SetLastRun(_ context.Context, name string, t time.Time) error {
	f.mu.Lock()
	f.last[name] = t
	f.mu.Unlock()
	atomic.AddInt32(&f.sets, 1)
	return nil
}

func TestPersistentSkipsRecentRun(t *testing.T) {
	s := scheduler.New(nil)
	s.SetStore(&fakeStore{last: map[string]time.Time{"dispatch": time.Now()}})
	var runs int32
	s.Add(scheduler.Job{
		Name:       "dispatch",
		Interval:   time.Hour,
		RunOnStart: true,
		Persistent: true,
		Run:        func(context.Context) error { atomic.AddInt32(&runs, 1); return nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	s.Start(ctx)
	<-ctx.Done()
	if got := atomic.LoadInt32(&runs); got != 0 {
		t.Fatalf("persistent job ran %d times on start despite a recent run, want 0", got)
	}
}

func TestPersistentRunsWhenDueAndRecords(t *testing.T) {
	fs := &fakeStore{last: map[string]time.Time{"dispatch": time.Now().Add(-2 * time.Hour)}}
	s := scheduler.New(nil)
	s.SetStore(fs)
	var runs int32
	s.Add(scheduler.Job{
		Name:       "dispatch",
		Interval:   time.Hour,
		RunOnStart: true,
		Persistent: true,
		Run:        func(context.Context) error { atomic.AddInt32(&runs, 1); return nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	s.Start(ctx)
	<-ctx.Done()
	if got := atomic.LoadInt32(&runs); got < 1 {
		t.Fatalf("persistent job ran %d times, want >= 1 (overdue)", got)
	}
	if got := atomic.LoadInt32(&fs.sets); got < 1 {
		t.Fatal("persistent job did not record its run")
	}
}
