package store

import (
	"context"
	"fmt"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// UpsertWorker registers or updates a worker and refreshes its last_seen.
func (s *Store) UpsertWorker(ctx context.Context, w model.Worker) error {
	const q = `
		INSERT INTO workers (id, capacity, status, last_seen)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE
		SET capacity = EXCLUDED.capacity, status = EXCLUDED.status, last_seen = now()`
	if _, err := s.pool.Exec(ctx, q, w.ID, w.Capacity, w.Status); err != nil {
		return fmt.Errorf("upsert worker %s: %w", w.ID, err)
	}
	return nil
}

// Heartbeat refreshes a worker's status and last_seen timestamp.
func (s *Store) Heartbeat(ctx context.Context, workerID, status string) error {
	const q = `UPDATE workers SET status = $2, last_seen = now() WHERE id = $1`
	if _, err := s.pool.Exec(ctx, q, workerID, status); err != nil {
		return fmt.Errorf("heartbeat %s: %w", workerID, err)
	}
	return nil
}

// AliveWorkers counts workers seen within the given window. The coordinator uses
// it to cap fan-out: there is no point creating more distinct-worker slots than
// there are live workers to claim them.
func (s *Store) AliveWorkers(ctx context.Context, within time.Duration) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM workers WHERE last_seen >= $1`, time.Now().Add(-within),
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("alive workers: %w", err)
	}
	return n, nil
}

// FleetStats is a snapshot of fleet and queue health for operational metrics.
type FleetStats struct {
	Workers int
	Alive   int
	Queued  int
	Claimed int
	Done    int
	Failed  int
}

// Fleet returns a one-shot metrics snapshot of the fleet and job queue.
func (s *Store) Fleet(ctx context.Context, aliveWindow time.Duration) (FleetStats, error) {
	var fs FleetStats
	const q = `
		SELECT
			(SELECT count(*) FROM workers),
			(SELECT count(*) FROM workers WHERE last_seen >= $1),
			(SELECT count(*) FROM jobs WHERE state = 'queued'),
			(SELECT count(*) FROM jobs WHERE state = 'claimed'),
			(SELECT count(*) FROM jobs WHERE state = 'done'),
			(SELECT count(*) FROM jobs WHERE state = 'failed')`
	if err := s.pool.QueryRow(ctx, q, time.Now().Add(-aliveWindow)).Scan(
		&fs.Workers, &fs.Alive, &fs.Queued, &fs.Claimed, &fs.Done, &fs.Failed,
	); err != nil {
		return fs, fmt.Errorf("fleet stats: %w", err)
	}
	return fs, nil
}

// ListWorkers returns the whole fleet.
func (s *Store) ListWorkers(ctx context.Context) ([]model.Worker, error) {
	const q = `SELECT id, capacity, status, COALESCE(last_seen, 'epoch') FROM workers ORDER BY id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()

	var out []model.Worker
	for rows.Next() {
		var w model.Worker
		if err := rows.Scan(&w.ID, &w.Capacity, &w.Status, &w.LastSeen); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
