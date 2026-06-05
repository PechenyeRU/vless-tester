package scheduler_test

import (
	"context"
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
