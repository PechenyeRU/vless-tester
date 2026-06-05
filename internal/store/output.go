package store

import "context"

// OutputFilter holds the publish-time output knobs: a node-name prefix, a
// success-limit cap, and name-regex include/exclude filters.
type OutputFilter struct {
	NodePrefix   string
	SuccessLimit int
	NameInclude  string
	NameExclude  string
}

// OutputSettings reads the publish-time output filters from settings. Missing
// keys default to the zero value (no prefix, no limit, no filtering).
func (s *Store) OutputSettings(ctx context.Context) (OutputFilter, error) {
	var of OutputFilter
	_ = s.GetSetting(ctx, "output.node_prefix", &of.NodePrefix)
	_ = s.GetSetting(ctx, "output.success_limit", &of.SuccessLimit)
	_ = s.GetSetting(ctx, "filter.name_include", &of.NameInclude)
	_ = s.GetSetting(ctx, "filter.name_exclude", &of.NameExclude)
	return of, nil
}
