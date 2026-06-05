package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// NotifySettings returns the end-of-cycle notification config: whether it is
// enabled and the list of shoutrrr service URLs. Missing settings mean disabled
// with no URLs.
func (s *Store) NotifySettings(ctx context.Context) (enabled bool, urls []string, err error) {
	if err = s.GetSetting(ctx, "notify.enabled", &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		} else {
			return false, nil, err
		}
	}
	if err = s.GetSetting(ctx, "notify.urls", &urls); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return enabled, nil, nil
		}
		return enabled, nil, err
	}
	return enabled, urls, nil
}
