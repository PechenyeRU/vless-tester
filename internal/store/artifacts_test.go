package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/whitedns/vless-tester/internal/store"
)

func TestPublishedArtifactRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.PublishedArtifact(ctx, "clash"); !errors.Is(err, store.ErrNoArtifact) {
		t.Fatalf("missing artifact = %v, want ErrNoArtifact", err)
	}

	if err := st.SavePublishedArtifact(ctx, "clash", "text/yaml", []byte("proxies: []\n"), 3); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.PublishedArtifact(ctx, "clash")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Content) != "proxies: []\n" || got.ContentType != "text/yaml" || got.NodeCount != 3 {
		t.Fatalf("got %+v", got)
	}

	// Upsert replaces content, type and count for the same target.
	if err := st.SavePublishedArtifact(ctx, "clash", "application/yaml", []byte("proxies: [a]\n"), 5); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err = st.PublishedArtifact(ctx, "clash")
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if string(got.Content) != "proxies: [a]\n" || got.ContentType != "application/yaml" || got.NodeCount != 5 {
		t.Fatalf("after upsert got %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updated_at not set")
	}
}
