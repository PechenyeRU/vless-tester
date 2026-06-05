-- Fan-out & batch-scoped jobs. A config is validated by exactly N distinct
-- workers (DESIGN 5), so the queue holds up to N open assignments per
-- (server, phase): one per slot. Each job also carries the batch (cycle) that
-- created it, so a worker's result inherits the batch tag for re-gating.

ALTER TABLE jobs ADD COLUMN slot     INT NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN batch_id BIGINT REFERENCES batches(id) ON DELETE CASCADE;

-- Replace the one-open-job-per-(server,phase) rule with one-per-slot, so N
-- assignments can be open at once while still preventing duplicate slots.
DROP INDEX jobs_open_unique_idx;
CREATE UNIQUE INDEX jobs_open_unique_idx ON jobs (server_id, phase, slot)
    WHERE state IN ('queued', 'claimed');

CREATE INDEX jobs_batch_idx ON jobs (batch_id);
