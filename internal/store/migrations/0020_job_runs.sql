-- Records the last run time of persistent scheduler jobs (e.g. dispatch), so a
-- coordinator restart resumes the schedule instead of re-firing the job on every
-- boot. Keyed by the job name; one row per persistent job.
CREATE TABLE IF NOT EXISTS job_runs (
    name     TEXT PRIMARY KEY,
    last_run TIMESTAMPTZ NOT NULL
);
