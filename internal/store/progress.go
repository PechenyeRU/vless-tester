package store

import (
	"context"
	"fmt"
	"time"
)

// CycleProgress summarizes the in-flight test cycle (the latest unfinished
// batch): how many jobs it holds and how many have completed, plus when it
// started, so the UI can show a progress bar and an ETA.
type CycleProgress struct {
	Active    bool
	BatchID   int64
	Total     int
	Done      int
	Failed    int
	Open      int
	StartedAt time.Time
}

// CycleProgress returns the progress of the active cycle. Active is false when no
// batch is in flight (idle).
func (s *Store) CycleProgress(ctx context.Context) (CycleProgress, error) {
	id, active, err := s.LatestUnfinishedBatch(ctx)
	if err != nil {
		return CycleProgress{}, fmt.Errorf("cycle progress: latest batch: %w", err)
	}
	if !active {
		return CycleProgress{Active: false}, nil
	}
	cp := CycleProgress{Active: true, BatchID: id}
	const q = `
		SELECT b.started_at,
		       count(j.id),
		       count(j.id) FILTER (WHERE j.state = 'done'),
		       count(j.id) FILTER (WHERE j.state = 'failed'),
		       count(j.id) FILTER (WHERE j.state IN ('queued', 'claimed'))
		FROM batches b
		LEFT JOIN jobs j ON j.batch_id = b.id
		WHERE b.id = $1
		GROUP BY b.started_at`
	if err := s.pool.QueryRow(ctx, q, id).Scan(
		&cp.StartedAt, &cp.Total, &cp.Done, &cp.Failed, &cp.Open,
	); err != nil {
		return cp, fmt.Errorf("cycle progress: counts: %w", err)
	}
	return cp, nil
}
