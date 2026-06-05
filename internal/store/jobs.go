package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// NackJobs releases the given jobs back to the queue, but only those still
// claimed by this worker. A worker is untrusted, so it can never affect jobs it
// does not hold. It returns the number of jobs actually requeued.
func (s *Store) NackJobs(ctx context.Context, workerID string, jobIDs []int64) (int64, error) {
	if len(jobIDs) == 0 {
		return 0, nil
	}
	const q = `
		UPDATE jobs
		SET state = 'queued', claimed_by = NULL, claimed_at = NULL
		WHERE state = 'claimed' AND claimed_by = $1 AND id = ANY($2)`
	tag, err := s.pool.Exec(ctx, q, workerID, jobIDs)
	if err != nil {
		return 0, fmt.Errorf("nack jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RecordResult persists a worker's measurement for a job and closes the job, all
// in one transaction. The worker reports only a job_id; the coordinator resolves
// the server and phase from the job itself and verifies the job is still claimed
// by this worker, so an untrusted worker can neither pick the server nor report
// for work it does not hold. ok is false (with no error) when the job is missing
// or not claimed by this worker, so a stale or forged report is silently
// dropped. The measurement is appended to the history with no batch tag; the
// distributed cycle batching is layered on top in T1.3.
func (s *Store) RecordResult(ctx context.Context, workerID string, jobID int64, r model.TestRun) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("record result: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var serverID int64
	var phase string
	err = tx.QueryRow(ctx,
		`SELECT server_id, phase FROM jobs
		 WHERE id = $1 AND state = 'claimed' AND claimed_by = $2 FOR UPDATE`,
		jobID, workerID,
	).Scan(&serverID, &phase)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("record result: lookup job %d: %w", jobID, err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO test_runs (server_id, worker_id, batch_id, phase, latency_ms, dl_mbps, ul_mbps, status, error)
		 VALUES ($1, $2, NULL, $3, $4, $5, $6, $7, $8)`,
		serverID, workerID, phase, r.LatencyMs, r.DlMbps, r.UlMbps, string(r.Status), nullString(r.Error),
	); err != nil {
		return false, fmt.Errorf("record result: insert run: %w", err)
	}

	state := model.JobDone
	if r.Status != model.StatusOK {
		state = model.JobFailed
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET state = $2 WHERE id = $1`, jobID, string(state)); err != nil {
		return false, fmt.Errorf("record result: close job %d: %w", jobID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("record result: commit: %w", err)
	}
	return true, nil
}

// CountJobs counts jobs in a given state.
func (s *Store) CountJobs(ctx context.Context, state model.JobState) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE state = $1`, string(state)).Scan(&n); err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return n, nil
}
