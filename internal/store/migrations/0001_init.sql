-- Initial schema for the vless-tester control plane. See DESIGN.md section 5.

CREATE TABLE servers (
    id           BIGSERIAL PRIMARY KEY,
    fingerprint  TEXT NOT NULL UNIQUE,          -- hash(protocol+host+port+cred+transport)
    raw_uri      TEXT NOT NULL,                 -- original share link
    protocol     TEXT NOT NULL,                 -- vless|vmess|hysteria2|tuic|trojan|ss
    host         TEXT NOT NULL,
    port         INT  NOT NULL,
    country      TEXT,                          -- ISO-3166 alpha-2
    seq_name     TEXT,                          -- stable per-country name, e.g. FR110
    first_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workers (
    id            TEXT PRIMARY KEY,             -- mnemonic name, ^[A-Za-z0-9-]+$
    capacity      JSONB NOT NULL,               -- baseline-measured: {latency, speed, bw_mbps}
    status        TEXT NOT NULL DEFAULT 'idle',
    last_seen     TIMESTAMPTZ
);

CREATE TABLE jobs (
    id          BIGSERIAL PRIMARY KEY,
    server_id   BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    phase       TEXT NOT NULL,                  -- latency|speed|checks
    state       TEXT NOT NULL DEFAULT 'queued', -- queued|claimed|done|failed
    claimed_by  TEXT REFERENCES workers(id) ON DELETE SET NULL,
    claimed_at  TIMESTAMPTZ,
    attempts    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX jobs_state_phase_idx ON jobs (state, phase);
-- A server has at most one open job per phase; avoids duplicate enqueues.
CREATE UNIQUE INDEX jobs_open_unique_idx ON jobs (server_id, phase)
    WHERE state IN ('queued', 'claimed');

CREATE TABLE test_runs (
    id          BIGSERIAL PRIMARY KEY,
    server_id   BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    worker_id   TEXT   NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    phase       TEXT   NOT NULL,
    run_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    latency_ms  INT,
    dl_mbps     REAL,
    ul_mbps     REAL,
    status      TEXT NOT NULL,                  -- ok|timeout|error|refused
    error       TEXT
);
CREATE INDEX test_runs_server_run_idx ON test_runs (server_id, run_at DESC);

CREATE TABLE checks (
    id        BIGSERIAL PRIMARY KEY,
    run_id    BIGINT REFERENCES test_runs(id) ON DELETE CASCADE,
    server_id BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name      TEXT NOT NULL,                    -- reach_youtube|geo_match|dns_leak|...
    passed    BOOLEAN NOT NULL,
    metric    REAL,
    detail    TEXT
);

CREATE TABLE sources (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,                   -- raw_file|subscription_url
    location   TEXT NOT NULL UNIQUE,            -- path or URL
    last_fetch TIMESTAMPTZ,
    enabled    BOOLEAN NOT NULL DEFAULT true
);

-- Coordinator runtime preferences, editable from the admin UI.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed defaults (idempotent; UI overrides take precedence afterwards).
INSERT INTO settings (key, value) VALUES
    ('publish.interval',     '"12h"'),
    ('publish.github_repo',  '""'),
    ('sources.refresh',      '"1h"'),
    ('geoip.refresh',        '"336h"'),
    ('approval.min_dl_mbps', '1'),
    ('approval.max_latency_ms', '800'),
    ('speed.adaptive',       'true'),
    ('speed.streams',        '6'),
    ('speed.bytes',          '10000000')
ON CONFLICT (key) DO NOTHING;
