package store

import "context"

// DefaultMaxProbes caps how many servers a single dispatch cycle enqueues when
// the dispatch.max_probes setting is absent. A large source set can resolve to
// hundreds of thousands of servers; without a cap one cycle would enqueue them
// all at once, ballooning the queue and the coordinator's memory. An explicit
// dispatch.max_probes of 0 still means "unlimited" for operators who want it.
const DefaultMaxProbes = 5000

// DispatchSettings returns the per-cycle dispatch knobs: whether to shuffle the
// server order and a cap on servers tested per run. A missing dispatch.max_probes
// defaults to DefaultMaxProbes (not unlimited); an explicit 0 means unlimited.
// shuffle defaults to false.
func (s *Store) DispatchSettings(ctx context.Context) (shuffle bool, maxProbes int, err error) {
	maxProbes = DefaultMaxProbes
	_ = s.GetSetting(ctx, "dispatch.shuffle", &shuffle)
	_ = s.GetSetting(ctx, "dispatch.max_probes", &maxProbes)
	return shuffle, maxProbes, nil
}
