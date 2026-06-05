package store

import "context"

// DispatchSettings returns the per-cycle dispatch knobs: whether to shuffle the
// server order and a cap on servers tested per run (0 = unlimited). Missing keys
// default to no shuffle / no cap.
func (s *Store) DispatchSettings(ctx context.Context) (shuffle bool, maxProbes int, err error) {
	_ = s.GetSetting(ctx, "dispatch.shuffle", &shuffle)
	_ = s.GetSetting(ctx, "dispatch.max_probes", &maxProbes)
	return shuffle, maxProbes, nil
}
