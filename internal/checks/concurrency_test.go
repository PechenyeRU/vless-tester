package checks

import (
	"context"
	"testing"
	"time"
)

func TestSemaphoreCapsConcurrency(t *testing.T) {
	s := NewSemaphore(2)
	ctx := context.Background()

	if err := s.Acquire(ctx); err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	if err := s.Acquire(ctx); err != nil {
		t.Fatalf("acquire 2: %v", err)
	}

	// The third acquire must block; with an already-canceled context it errors.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Acquire(canceled); err == nil {
		t.Fatal("third acquire should fail when full and context is canceled")
	}

	s.Release()
	if err := s.Acquire(ctx); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestBandwidthBudgetConsumeAndRefill(t *testing.T) {
	b := NewBandwidthBudget(1000, 5000) // 1000 B/s refill, 5000 B capacity

	// Drive the clock deterministically.
	fake := time.Unix(0, 0)
	b.clock = func() time.Time { return fake }
	b.last = fake

	if !b.Allow(5000) {
		t.Fatal("should allow consuming the full bucket")
	}
	if b.Allow(1) {
		t.Fatal("bucket is empty; further consumption must be denied")
	}

	fake = fake.Add(time.Second) // accrue 1000 tokens
	if !b.Allow(1000) {
		t.Fatal("should allow after one second of refill")
	}
	if b.Allow(1) {
		t.Fatal("only 1000 tokens accrued; the next byte must be denied")
	}
}

func TestBandwidthBudgetCapsAtCapacity(t *testing.T) {
	b := NewBandwidthBudget(1000, 5000)
	fake := time.Unix(0, 0)
	b.clock = func() time.Time { return fake }
	b.last = fake

	if !b.Allow(5000) {
		t.Fatal("drain bucket")
	}
	fake = fake.Add(time.Hour) // would accrue far more than capacity
	if !b.Allow(5000) {
		t.Fatal("refill must restore up to capacity")
	}
	if b.Allow(1) {
		t.Fatal("tokens must be capped at capacity, not overfilled")
	}
}
