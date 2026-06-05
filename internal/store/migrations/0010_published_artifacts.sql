-- Rendered subscription artifacts, one row per output format. The coordinator
-- writes these at publish time so the public GET /sub endpoint can serve the
-- latest working list without recomputing it on every request.
CREATE TABLE IF NOT EXISTS published_artifacts (
    target       TEXT PRIMARY KEY,
    content      BYTEA NOT NULL,
    content_type TEXT NOT NULL,
    node_count   INTEGER NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
