-- Speed up the worker claim. ClaimJobs scans queued jobs in created_at order and
-- excludes any (server, phase) this worker already tested (the distinct-worker
-- anti-join). With a large queue and no supporting index the planner seq-scans
-- the whole jobs table and sorts every queued row on each claim (spilling to
-- disk), which throttles fleet throughput far below the worker concurrency.
--
-- The partial index on created_at over queued rows lets the claim walk the queue
-- in order and stop early at the LIMIT (no sort); the second index makes the
-- per-row anti-join lookup cheap, so the join can stream instead of hashing the
-- whole queued set.
CREATE INDEX IF NOT EXISTS jobs_queued_created_idx ON jobs (created_at) WHERE state = 'queued';
CREATE INDEX IF NOT EXISTS jobs_sib_idx ON jobs (server_id, phase, claimed_by) WHERE state IN ('claimed', 'done', 'failed');
