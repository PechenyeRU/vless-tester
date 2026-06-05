package store

import (
	"context"
	"fmt"

	"github.com/whitedns/vless-tester/internal/model"
)

// UpsertSource adds or re-enables an ingest source keyed by its location.
func (s *Store) UpsertSource(ctx context.Context, kind model.SourceKind, location string) error {
	const q = `
		INSERT INTO sources (kind, location)
		VALUES ($1, $2)
		ON CONFLICT (location) DO UPDATE SET kind = EXCLUDED.kind, enabled = true`
	if _, err := s.pool.Exec(ctx, q, string(kind), location); err != nil {
		return fmt.Errorf("upsert source: %w", err)
	}
	return nil
}

// ListSources returns the enabled ingest sources.
func (s *Store) ListSources(ctx context.Context) ([]model.Source, error) {
	const q = `SELECT id, kind, location, last_fetch, enabled FROM sources WHERE enabled ORDER BY id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	var out []model.Source
	for rows.Next() {
		var src model.Source
		var kind string
		if err := rows.Scan(&src.ID, &kind, &src.Location, &src.LastFetch, &src.Enabled); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		src.Kind = model.SourceKind(kind)
		out = append(out, src)
	}
	return out, rows.Err()
}

// TouchSource records that a source was just fetched.
func (s *Store) TouchSource(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `UPDATE sources SET last_fetch = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("touch source %d: %w", id, err)
	}
	return nil
}
