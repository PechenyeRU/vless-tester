-- Per-worker authentication tokens, created and revoked from the admin panel.
-- Each token carries a worker name (its identity on the control plane). Only the
-- sha256 of the secret is stored; the plaintext is shown once at creation.
-- Deleting a row revokes the token.
CREATE TABLE worker_tokens (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,            -- worker identity, ^[A-Za-z0-9-]+$
    token_hash TEXT NOT NULL UNIQUE,            -- sha256 hex of the secret
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used  TIMESTAMPTZ
);
