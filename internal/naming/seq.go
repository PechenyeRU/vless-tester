package naming

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// FormatSeqName builds a provider-style sequence name from a country code and an
// index, e.g. ("fr", 110) -> "FR110".
func FormatSeqName(country string, index int) string {
	return fmt.Sprintf("%s%d", strings.ToUpper(country), index)
}

// SeqBackend persists the fingerprint -> sequence-name mapping and the per
// country counter. The store provides a Postgres-backed implementation; tests
// use MemoryBackend.
type SeqBackend interface {
	// Lookup returns the existing name for a fingerprint, ok=false if unassigned.
	Lookup(ctx context.Context, fingerprint string) (name string, ok bool, err error)
	// NextIndex atomically reserves and returns the next index for a country.
	NextIndex(ctx context.Context, country string) (int, error)
	// Save records the fingerprint -> name assignment.
	Save(ctx context.Context, fingerprint, name string) error
}

// Allocator assigns stable per-country sequence names. A given fingerprint keeps
// its name across runs, just like a VPN provider's server numbering.
type Allocator struct {
	Backend SeqBackend
}

// Assign returns the stable sequence name for a server fingerprint, allocating a
// new one on first sight.
func (a Allocator) Assign(ctx context.Context, fingerprint, country string) (string, error) {
	if name, ok, err := a.Backend.Lookup(ctx, fingerprint); err != nil {
		return "", err
	} else if ok {
		return name, nil
	}
	idx, err := a.Backend.NextIndex(ctx, country)
	if err != nil {
		return "", err
	}
	name := FormatSeqName(country, idx)
	if err := a.Backend.Save(ctx, fingerprint, name); err != nil {
		return "", err
	}
	return name, nil
}

// MemoryBackend is an in-memory SeqBackend for tests and single-process runs.
// Counters start at 1 for each country.
type MemoryBackend struct {
	mu       sync.Mutex
	names    map[string]string
	counters map[string]int
}

// NewMemoryBackend creates an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{names: map[string]string{}, counters: map[string]int{}}
}

func (m *MemoryBackend) Lookup(_ context.Context, fingerprint string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name, ok := m.names[fingerprint]
	return name, ok, nil
}

func (m *MemoryBackend) NextIndex(_ context.Context, country string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	country = strings.ToUpper(country)
	m.counters[country]++
	return m.counters[country], nil
}

func (m *MemoryBackend) Save(_ context.Context, fingerprint, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.names[fingerprint] = name
	return nil
}
