package store

import "context"

// SubPath returns the optional obfuscated path token for the public /sub
// endpoint. Empty (the default) means /sub is served at the bare path.
func (s *Store) SubPath(ctx context.Context) (string, error) {
	var path string
	_ = s.GetSetting(ctx, "sub.path", &path)
	return path, nil
}
