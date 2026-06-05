package store

import (
	"context"
	"fmt"

	"github.com/whitedns/vless-tester/internal/model"
)

// InsertTestRun records one measurement and returns its ID.
func (s *Store) InsertTestRun(ctx context.Context, r model.TestRun) (int64, error) {
	const q = `
		INSERT INTO test_runs (server_id, worker_id, phase, latency_ms, dl_mbps, ul_mbps, status, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`
	var id int64
	if err := s.pool.QueryRow(ctx, q,
		r.ServerID, r.WorkerID, string(r.Phase), r.LatencyMs, r.DlMbps, r.UlMbps,
		string(r.Status), nullString(r.Error),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert test run: %w", err)
	}
	return id, nil
}

// InsertCheck records an extensible approval-check result.
func (s *Store) InsertCheck(ctx context.Context, c model.Check) (int64, error) {
	const q = `
		INSERT INTO checks (run_id, server_id, name, passed, metric, detail)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`
	var id int64
	if err := s.pool.QueryRow(ctx, q,
		c.RunID, c.ServerID, c.Name, c.Passed, c.Metric, nullString(c.Detail),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert check: %w", err)
	}
	return id, nil
}

// nullString maps an empty string to NULL so optional text columns stay clean.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
