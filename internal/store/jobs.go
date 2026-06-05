package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/whitedns/vless-tester/internal/model"
)

// EnqueueJob queues a single (slot 0) job for a server/phase. The partial unique
// index keeps at most one open job per (server, phase, slot), so re-enqueuing is
// a no-op and the returned bool reports whether a new job was created.
func (s *Store) EnqueueJob(ctx context.Context, serverID int64, phase model.JobPhase) (bool, error) {
	const q = `
		INSERT INTO jobs (server_id, phase, slot)
		VALUES ($1, $2, 0)
		ON CONFLICT (server_id, phase, slot) WHERE state IN ('queued', 'claimed')
		DO NOTHING`
	tag, err := s.pool.Exec(ctx, q, serverID, string(phase))
	if err != nil {
		return false, fmt.Errorf("enqueue job: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// EnqueueFanout queues n open slots (0..n-1) for a (server, phase) within a
// batch, so the config is tested by up to n distinct workers (DESIGN 5). Slots
// that already have an open job are left untouched (idempotent re-dispatch). It
// returns how many new jobs were created.
func (s *Store) EnqueueFanout(ctx context.Context, batchID, serverID int64, phase model.JobPhase, n int) (int64, error) {
	if n < 1 {
		n = 1
	}
	const q = `
		INSERT INTO jobs (server_id, phase, slot, batch_id)
		SELECT $1, $2, g, $3 FROM generate_series(0, $4 - 1) g
		ON CONFLICT (server_id, phase, slot) WHERE state IN ('queued', 'claimed')
		DO NOTHING`
	tag, err := s.pool.Exec(ctx, q, serverID, string(phase), batchID, n)
	if err != nil {
		return 0, fmt.Errorf("enqueue fanout: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClaimJobs atomically leases up to max queued jobs (optionally filtered by
// phase) to a worker, using FOR UPDATE SKIP LOCKED so concurrent workers never
// grab the same job. To keep the N proofs distinct, a worker is never given a
// job for a (server, phase) it already holds or has already tested: this is how
// fan-out spreads each config across different workers (DESIGN 5). Claimed jobs
// are returned with their server raw_uri.
func (s *Store) ClaimJobs(ctx context.Context, workerID string, phase model.JobPhase, max int, protocols []string) ([]ClaimedJob, error) {
	phaseFilter := string(phase)
	const q = `
		WITH locked AS (
			-- Lock up to max claimable rows with the proven SKIP LOCKED pattern,
			-- excluding any config this worker already holds/tested (cross-call
			-- distinctness) and any protocol this worker may not test ($4, a
			-- per-worker allow-list; NULL means no restriction).
			SELECT j.id, j.server_id, j.phase, j.created_at
			FROM jobs j
			WHERE j.state = 'queued'
			  AND ($2 = '' OR j.phase = $2)
			  AND ($4::text[] IS NULL OR
			       (SELECT s.protocol FROM servers s WHERE s.id = j.server_id) = ANY($4))
			  AND NOT EXISTS (
			      SELECT 1 FROM jobs sib
			      WHERE sib.server_id = j.server_id AND sib.phase = j.phase
			        AND sib.claimed_by = $1
			        AND sib.state IN ('claimed', 'done', 'failed')
			  )
			ORDER BY j.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		),
		picked AS (
			-- Keep at most one slot per (server, phase) from this batch of locked
			-- rows, so a single claim never hands one worker two of a config's N
			-- proofs (within-call distinctness). Extra locked rows stay queued.
			SELECT DISTINCT ON (server_id, phase) id
			FROM locked
			ORDER BY server_id, phase, created_at
		)
		UPDATE jobs j
		SET state = 'claimed', claimed_by = $1, claimed_at = now(), attempts = attempts + 1
		FROM picked
		WHERE j.id = picked.id
		RETURNING j.id, j.server_id, j.phase,
		          (SELECT raw_uri FROM servers s WHERE s.id = j.server_id),
		          (SELECT protocol FROM servers s WHERE s.id = j.server_id)`
	// Pass NULL (not an empty array) when there is no restriction, so the
	// $4::text[] IS NULL branch short-circuits the protocol filter.
	var protoArg any
	if len(protocols) > 0 {
		protoArg = protocols
	}
	rows, err := s.pool.Query(ctx, q, workerID, phaseFilter, max, protoArg)
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

// RequeueExpired handles jobs whose lease has expired (claimed_at older than
// ttl), i.e. a worker that died or stalled mid-test. Such a job is returned to
// the queue so a *different* live worker can retry it (the distinct-worker claim
// rule keeps the N proofs distinct). A job that has already been attempted
// maxAttempts times is marked failed instead of looping forever; maxAttempts <= 0
// disables that cap. It returns how many jobs were requeued and failed.
func (s *Store) RequeueExpired(ctx context.Context, ttl time.Duration, maxAttempts int) (requeued, failed int64, err error) {
	cutoff := time.Now().Add(-ttl)

	if maxAttempts > 0 {
		tag, ferr := s.pool.Exec(ctx,
			`UPDATE jobs SET state = 'failed'
			 WHERE state = 'claimed' AND claimed_at < $1 AND attempts >= $2`,
			cutoff, maxAttempts)
		if ferr != nil {
			return 0, 0, fmt.Errorf("fail exhausted: %w", ferr)
		}
		failed = tag.RowsAffected()
	}

	tag, rerr := s.pool.Exec(ctx,
		`UPDATE jobs SET state = 'queued', claimed_by = NULL, claimed_at = NULL
		 WHERE state = 'claimed' AND claimed_at < $1`,
		cutoff)
	if rerr != nil {
		return 0, failed, fmt.Errorf("requeue expired: %w", rerr)
	}
	return tag.RowsAffected(), failed, nil
}

// OpenJobCount counts jobs still in flight (queued or claimed) for a batch, or
// across all batches when batchID is nil. A batch is drained (ready to publish)
// when this reaches zero.
func (s *Store) OpenJobCount(ctx context.Context, batchID *int64) (int, error) {
	const q = `
		SELECT count(*) FROM jobs
		WHERE state IN ('queued', 'claimed')
		  AND ($1::bigint IS NULL OR batch_id = $1)`
	var n int
	if err := s.pool.QueryRow(ctx, q, batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("open job count: %w", err)
	}
	return n, nil
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
	var batchID *int64
	err = tx.QueryRow(ctx,
		`SELECT server_id, phase, batch_id FROM jobs
		 WHERE id = $1 AND state = 'claimed' AND claimed_by = $2 FOR UPDATE`,
		jobID, workerID,
	).Scan(&serverID, &phase, &batchID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("record result: lookup job %d: %w", jobID, err)
	}

	var runID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO test_runs (server_id, worker_id, batch_id, phase, latency_ms, dl_mbps, ul_mbps, status, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		serverID, workerID, batchID, phase, r.LatencyMs, r.DlMbps, r.UlMbps, string(r.Status), nullString(r.Error),
	).Scan(&runID); err != nil {
		return false, fmt.Errorf("record result: insert run: %w", err)
	}

	// Extensible per-platform outcomes (media unlock, etc.) are linked to the run
	// so the admin UI can show them per worker (DESIGN: never in public output).
	for _, c := range r.Checks {
		if _, err := tx.Exec(ctx,
			`INSERT INTO checks (run_id, server_id, name, passed, metric, detail) VALUES ($1, $2, $3, $4, $5, $6)`,
			runID, serverID, c.Name, c.Passed, c.Metric, nullString(c.Detail),
		); err != nil {
			return false, fmt.Errorf("record result: insert check: %w", err)
		}
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
