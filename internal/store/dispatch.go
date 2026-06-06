package store

import "context"

// DispatchSettings returns the per-cycle dispatch knobs: whether to shuffle the
// server order and an optional cap on servers tested per run. Both default off
// (max_probes 0 = no cap, enqueue the whole catalog): enqueuing is bulk/set-based
// and capacity-aware claiming bounds what each worker actually pulls, so a large
// queue is fine. The cap remains as an optional brake for small fleets.
func (s *Store) DispatchSettings(ctx context.Context) (shuffle bool, maxProbes int, err error) {
	_ = s.GetSetting(ctx, "dispatch.shuffle", &shuffle)
	_ = s.GetSetting(ctx, "dispatch.max_probes", &maxProbes)
	return shuffle, maxProbes, nil
}
