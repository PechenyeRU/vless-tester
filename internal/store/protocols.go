package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// EnabledProtocols returns the set of protocols the coordinator should test,
// from the protocols.enabled setting. A missing or empty value means "all" and
// returns nil, which callers treat as no restriction.
func (s *Store) EnabledProtocols(ctx context.Context) ([]string, error) {
	var protocols []string
	if err := s.GetSetting(ctx, "protocols.enabled", &protocols); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if len(protocols) == 0 {
		return nil, nil
	}
	return protocols, nil
}
