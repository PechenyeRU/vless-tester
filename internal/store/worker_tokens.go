package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/whitedns/vless-tester/internal/model"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint failure
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// workerNamePattern constrains worker identities so they stay safe as node
// vantage labels and URL/log tokens.
var workerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ErrWorkerNameTaken is returned when a token already exists for the name.
var ErrWorkerNameTaken = errors.New("store: worker name already in use")

// hashToken is the lookup key stored for a secret (sha256 hex). Tokens are
// high-entropy random strings, so an unsalted hash is sufficient and lets the
// control plane resolve a presented bearer with a single indexed lookup.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateWorkerToken mints a token for a worker name and returns the plaintext
// secret. The secret is shown only here; only its hash is persisted.
func (s *Store) CreateWorkerToken(ctx context.Context, name string) (string, error) {
	if !workerNamePattern.MatchString(name) {
		return "", fmt.Errorf("store: invalid worker name %q (allowed: A-Z a-z 0-9 -)", name)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("store: generate token: %w", err)
	}
	token := "wt_" + base64.RawURLEncoding.EncodeToString(raw)

	_, err := s.pool.Exec(ctx,
		`INSERT INTO worker_tokens (name, token_hash) VALUES ($1, $2)`,
		name, hashToken(token),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrWorkerNameTaken
		}
		return "", fmt.Errorf("store: create worker token: %w", err)
	}
	return token, nil
}

// ListWorkerTokens returns token metadata (never the secret), newest first.
func (s *Store) ListWorkerTokens(ctx context.Context) ([]model.WorkerToken, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, enabled, created_at, last_used
		   FROM worker_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list worker tokens: %w", err)
	}
	defer rows.Close()
	var out []model.WorkerToken
	for rows.Next() {
		var t model.WorkerToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Enabled, &t.CreatedAt, &t.LastUsed); err != nil {
			return nil, fmt.Errorf("store: scan worker token: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ResolveWorkerToken maps a presented secret to its worker name. ok is false for
// an unknown or disabled token. A successful lookup stamps last_used.
func (s *Store) ResolveWorkerToken(ctx context.Context, token string) (string, bool, error) {
	var name string
	err := s.pool.QueryRow(ctx,
		`UPDATE worker_tokens SET last_used = now()
		   WHERE token_hash = $1 AND enabled
		   RETURNING name`,
		hashToken(token),
	).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: resolve worker token: %w", err)
	}
	return name, true, nil
}

// DeleteWorkerToken revokes a token by id. Revoked workers can no longer
// authenticate. Returns false when no row matched.
func (s *Store) DeleteWorkerToken(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM worker_tokens WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("store: delete worker token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
