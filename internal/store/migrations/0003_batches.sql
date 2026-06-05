-- A batch groups all test runs of one coordinator cycle, giving the history a
-- clean boundary so publishing can select "only the latest batch" while older
-- batches stay intact for retroactive re-gating.
CREATE TABLE batches (
    id          BIGSERIAL PRIMARY KEY,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    trigger     TEXT NOT NULL DEFAULT 'scheduled', -- scheduled|manual
    note        TEXT
);

ALTER TABLE test_runs
    ADD COLUMN batch_id BIGINT REFERENCES batches(id) ON DELETE SET NULL;

CREATE INDEX test_runs_batch_idx ON test_runs (batch_id);
