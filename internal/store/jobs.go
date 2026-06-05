package store

import (
	"context"
	"fmt"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// EnqueueJob queues a job for a server/phase. The partial unique index keeps at
// most one open job per (server, phase), so re-enqueuing is a no-op and the
// returned bool reports whether a new job was created.
func (s *Store) EnqueueJob(ctx context.Context, serverID int64, phase model.JobPhase) (bool, error) {
	const q = `
		INSERT INTO jobs (server_id, phase)
		VALUES ($1, $2)
		ON CONFLICT (server_id, phase) WHERE state IN ('queued', 'claimed')
		DO NOTHING`
	tag, err := s.pool.Exec(ctx, q, serverID, string(phase))
	if err != nil {
		return false, fmt.Errorf("enqueue job: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ClaimJobs atomically leases up to max queued jobs (optionally filtered by
// phase) to a worker, using FOR UPDATE SKIP LOCKED so concurrent workers never
// grab the same job. Claimed jobs are returned with their server raw_uri.
func (s *Store) ClaimJobs(ctx context.Context, workerID string, phase model.JobPhase, max int) ([]ClaimedJob, error) {
	phaseFilter := string(phase)
	const q = `
		WITH picked AS (
			SELECT j.id
			FROM jobs j
			WHERE j.state = 'queued'
			  AND ($2 = '' OR j.phase = $2)
			ORDER BY j.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		UPDATE jobs j
		SET state = 'claimed', claimed_by = $1, claimed_at = now(), attempts = attempts + 1
		FROM picked
		WHERE j.id = picked.id
		RETURNING j.id, j.server_id, j.phase,
		          (SELECT raw_uri FROM servers s WHERE s.id = j.server_id),
		          (SELECT protocol FROM servers s WHERE s.id = j.server_id)`
	rows, err := s.pool.Query(ctx, q, workerID, phaseFilter, max)
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	defer rows.Close()

	var out []ClaimedJob
	for rows.Next() {
		var cj ClaimedJob
		var ph, proto string
		if err := rows.Scan(&cj.JobID, &cj.ServerID, &ph, &cj.RawURI, &proto); err != nil {
			return nil, fmt.Errorf("scan claimed job: %w", err)
		}
		cj.Phase = model.JobPhase(ph)
		cj.Protocol = model.Protocol(proto)
		out = append(out, cj)
	}
	return out, rows.Err()
}

// ClaimedJob is a leased unit of work handed to a worker.
type ClaimedJob struct {
	JobID    int64
	ServerID int64
	Phase    model.JobPhase
	RawURI   string
	Protocol model.Protocol
}

// CompleteJob marks a job done.
func (s *Store) CompleteJob(ctx context.Context, jobID int64) error {
	return s.setJobState(ctx, jobID, model.JobDone)
}

// FailJob marks a job failed.
func (s *Store) FailJob(ctx context.Context, jobID int64) error {
	return s.setJobState(ctx, jobID, model.JobFailed)
}

func (s *Store) setJobState(ctx context.Context, jobID int64, state model.JobState) error {
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET state = $2 WHERE id = $1`, jobID, string(state)); err != nil {
		return fmt.Errorf("set job %d state %s: %w", jobID, state, err)
	}
	return nil
}

// RequeueExpired returns claimed jobs older than ttl back to the queue so a dead
// worker's work is retried. It returns the number of requeued jobs.
func (s *Store) RequeueExpired(ctx context.Context, ttl time.Duration) (int64, error) {
	const q = `
		UPDATE jobs
		SET state = 'queued', claimed_by = NULL, claimed_at = NULL
		WHERE state = 'claimed' AND claimed_at < $1`
	tag, err := s.pool.Exec(ctx, q, time.Now().Add(-ttl))
	if err != nil {
		return 0, fmt.Errorf("requeue expired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CountJobs counts jobs in a given state.
func (s *Store) CountJobs(ctx context.Context, state model.JobState) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE state = $1`, string(state)).Scan(&n); err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return n, nil
}
