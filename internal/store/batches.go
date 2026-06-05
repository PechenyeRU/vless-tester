package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CreateBatch opens a new test batch and returns its ID. trigger is "scheduled"
// or "manual".
func (s *Store) CreateBatch(ctx context.Context, trigger string) (int64, error) {
	var id int64
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO batches (trigger) VALUES ($1) RETURNING id`, trigger,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("create batch: %w", err)
	}
	return id, nil
}

// FinishBatch marks a batch complete.
func (s *Store) FinishBatch(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE batches SET finished_at = now() WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("finish batch %d: %w", id, err)
	}
	return nil
}

// LatestFinishedBatch returns the most recent completed batch ID, ok=false when
// none exist yet.
func (s *Store) LatestFinishedBatch(ctx context.Context) (int64, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM batches WHERE finished_at IS NOT NULL ORDER BY finished_at DESC LIMIT 1`,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("latest batch: %w", err)
	}
	return id, true, nil
}
