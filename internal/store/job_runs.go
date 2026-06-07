package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LastRun returns the recorded last-run time of a scheduler job. ok is false
// (with no error) when the job has never recorded a run. It implements the
// scheduler.Store interface so persistent jobs survive coordinator restarts.
func (s *Store) LastRun(ctx context.Context, name string) (time.Time, bool, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT last_run FROM job_runs WHERE name = $1`, name).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("job last run %q: %w", name, err)
	}
	return t, true, nil
}

// SetLastRun records that a scheduler job ran at t (upsert by name).
func (s *Store) SetLastRun(ctx context.Context, name string, t time.Time) error {
	const q = `
		INSERT INTO job_runs (name, last_run) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET last_run = EXCLUDED.last_run`
	if _, err := s.pool.Exec(ctx, q, name, t); err != nil {
		return fmt.Errorf("set job last run %q: %w", name, err)
	}
	return nil
}
