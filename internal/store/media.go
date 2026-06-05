package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/whitedns/vless-tester/internal/model"
)

// MediaChecks returns the media-unlock platforms workers should probe, read from
// the runtime settings (media.enabled gates media.platforms). Missing settings
// mean disabled, so it returns nil without error.
func (s *Store) MediaChecks(ctx context.Context) ([]string, error) {
	var enabled bool
	if err := s.GetSetting(ctx, "media.enabled", &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	var platforms []string
	if err := s.GetSetting(ctx, "media.platforms", &platforms); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return platforms, nil
}

// MediaRequire returns the platforms a server must unlock to be worth a speed
// test (the media.require setting). Empty/missing means no media gating, so the
// speed test always runs.
func (s *Store) MediaRequire(ctx context.Context) ([]string, error) {
	var require []string
	if err := s.GetSetting(ctx, "media.require", &require); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if len(require) == 0 {
		return nil, nil
	}
	return require, nil
}

// ServerChecks returns the latest extensible check outcome per platform for a
// server (most recent run wins), for the admin detail view.
func (s *Store) ServerChecks(ctx context.Context, serverID int64) ([]model.CheckOutcome, error) {
	const q = `
		SELECT DISTINCT ON (c.name) c.name, c.passed, COALESCE(c.detail, '')
		FROM checks c
		JOIN test_runs r ON r.id = c.run_id
		WHERE c.server_id = $1
		ORDER BY c.name, r.run_at DESC, c.id DESC`
	rows, err := s.pool.Query(ctx, q, serverID)
	if err != nil {
		return nil, fmt.Errorf("server checks: %w", err)
	}
	defer rows.Close()
	var out []model.CheckOutcome
	for rows.Next() {
		var c model.CheckOutcome
		if err := rows.Scan(&c.Name, &c.Passed, &c.Detail); err != nil {
			return nil, fmt.Errorf("scan check: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
