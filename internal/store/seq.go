package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SeqBackend is a Postgres-backed sequence-name allocator. It structurally
// satisfies naming.SeqBackend without store importing the naming package.
type SeqBackend struct {
	s *Store
}

// NewSeqBackend returns a sequence-name backend bound to this store.
func (s *Store) NewSeqBackend() *SeqBackend { return &SeqBackend{s: s} }

// Lookup returns the existing sequence name for a fingerprint, if assigned.
func (b *SeqBackend) Lookup(ctx context.Context, fingerprint string) (string, bool, error) {
	var name string
	err := b.s.pool.QueryRow(ctx,
		`SELECT seq_name FROM servers WHERE fingerprint = $1 AND seq_name IS NOT NULL AND seq_name <> ''`,
		fingerprint,
	).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("seq lookup: %w", err)
	}
	return name, true, nil
}

// NextIndex atomically reserves and returns the next index for a country.
func (b *SeqBackend) NextIndex(ctx context.Context, country string) (int, error) {
	const q = `
		INSERT INTO country_seq (country, next_index) VALUES ($1, 1)
		ON CONFLICT (country) DO UPDATE SET next_index = country_seq.next_index + 1
		RETURNING next_index`
	var idx int
	if err := b.s.pool.QueryRow(ctx, q, country).Scan(&idx); err != nil {
		return 0, fmt.Errorf("seq next index: %w", err)
	}
	return idx, nil
}

// Save records the fingerprint -> sequence-name assignment on the server row.
func (b *SeqBackend) Save(ctx context.Context, fingerprint, name string) error {
	if _, err := b.s.pool.Exec(ctx,
		`UPDATE servers SET seq_name = $2 WHERE fingerprint = $1`, fingerprint, name,
	); err != nil {
		return fmt.Errorf("seq save: %w", err)
	}
	return nil
}
