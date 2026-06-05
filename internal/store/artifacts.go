package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNoArtifact is returned when a requested subscription target has not been
// published yet.
var ErrNoArtifact = errors.New("store: no published artifact")

// PublishedArtifact is a rendered subscription in one output format.
type PublishedArtifact struct {
	Target      string
	Content     []byte
	ContentType string
	NodeCount   int
	UpdatedAt   time.Time
}

// SavePublishedArtifact upserts the rendered content for one subscription
// target. It is called once per format at publish time.
func (s *Store) SavePublishedArtifact(ctx context.Context, target, contentType string, content []byte, nodeCount int) error {
	const q = `
		INSERT INTO published_artifacts (target, content, content_type, node_count, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (target) DO UPDATE
		SET content = EXCLUDED.content,
		    content_type = EXCLUDED.content_type,
		    node_count = EXCLUDED.node_count,
		    updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, target, content, contentType, nodeCount); err != nil {
		return fmt.Errorf("save artifact %s: %w", target, err)
	}
	return nil
}

// PublishedArtifact returns the latest rendered content for a target, or
// ErrNoArtifact if it has never been published.
func (s *Store) PublishedArtifact(ctx context.Context, target string) (PublishedArtifact, error) {
	var a PublishedArtifact
	const q = `SELECT target, content, content_type, node_count, updated_at
	           FROM published_artifacts WHERE target = $1`
	err := s.pool.QueryRow(ctx, q, target).Scan(&a.Target, &a.Content, &a.ContentType, &a.NodeCount, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedArtifact{}, ErrNoArtifact
	}
	if err != nil {
		return PublishedArtifact{}, fmt.Errorf("get artifact %s: %w", target, err)
	}
	return a, nil
}
