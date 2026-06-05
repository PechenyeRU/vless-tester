package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/whitedns/vless-tester/internal/store"
)

func TestWorkerTokenLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	token, err := st.CreateWorkerToken(ctx, "home-vps")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty secret")
	}

	// The secret resolves to its worker name; a wrong secret does not.
	name, ok, err := st.ResolveWorkerToken(ctx, token)
	if err != nil || !ok {
		t.Fatalf("resolve valid: ok=%v err=%v", ok, err)
	}
	if name != "home-vps" {
		t.Fatalf("resolved name = %q, want home-vps", name)
	}
	if _, ok, _ := st.ResolveWorkerToken(ctx, "wt_bogus"); ok {
		t.Fatal("bogus token resolved")
	}

	// Listing never leaks the secret and reflects the new token.
	list, err := st.ListWorkerTokens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "home-vps" || !list[0].Enabled {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Deleting revokes it: the same secret no longer authenticates.
	deleted, err := st.DeleteWorkerToken(ctx, list[0].ID)
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, ok, _ := st.ResolveWorkerToken(ctx, token); ok {
		t.Fatal("revoked token still resolves")
	}
}

func TestWorkerTokenRejectsDuplicateAndBadName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.CreateWorkerToken(ctx, "dup"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := st.CreateWorkerToken(ctx, "dup"); !errors.Is(err, store.ErrWorkerNameTaken) {
		t.Fatalf("want ErrWorkerNameTaken, got %v", err)
	}
	if _, err := st.CreateWorkerToken(ctx, "bad name!"); err == nil {
		t.Fatal("expected invalid-name error")
	}
}
