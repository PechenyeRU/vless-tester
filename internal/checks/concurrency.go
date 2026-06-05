package checks

import (
	"context"
	"sync"
	"time"
)

// Semaphore is a counting semaphore used to cap how many speed tests a worker
// runs at once (speed measurement is bandwidth-sensitive and must not overlap
// freely). Acquire honors context cancellation.
type Semaphore struct {
	tokens chan struct{}
}

// NewSemaphore creates a semaphore permitting n concurrent holders (min 1).
func NewSemaphore(n int) *Semaphore {
	if n < 1 {
		n = 1
	}
	return &Semaphore{tokens: make(chan struct{}, n)}
}

// Acquire blocks until a slot is free or ctx is done.
func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.tokens <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a slot. It must be called once per successful Acquire.
func (s *Semaphore) Release() { <-s.tokens }

// BandwidthBudget is a byte token bucket bounding how much data a worker pulls
// over time, so speed tests respect a metered link. A speed test asks Allow for
// its estimated byte cost before running; when the budget is exhausted the
// worker defers the job rather than blowing the cap.
type BandwidthBudget struct {
	mu           sync.Mutex
	capacity     float64
	tokens       float64
	refillPerSec float64
	last         time.Time
	clock        func() time.Time
}

// NewBandwidthBudget starts a full bucket of capacity bytes that refills at
// refillPerSec bytes per second.
func NewBandwidthBudget(refillPerSec, capacity float64) *BandwidthBudget {
	return &BandwidthBudget{
		capacity:     capacity,
		tokens:       capacity,
		refillPerSec: refillPerSec,
		last:         time.Now(),
		clock:        time.Now,
	}
}

// Allow consumes `bytes` tokens if available, returning true. Otherwise it
// leaves the bucket untouched and returns false.
func (b *BandwidthBudget) Allow(bytes float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens >= bytes {
		b.tokens -= bytes
		return true
	}
	return false
}

// refill adds tokens accrued since the last update, capped at capacity.
func (b *BandwidthBudget) refill() {
	now := b.clock()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.refillPerSec
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
}
