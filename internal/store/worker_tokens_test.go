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

	token, err := st.CreateWorkerToken(ctx, "home-vps", []string{"vless", "trojan"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty secret")
	}

	// The secret resolves to its worker name and per-worker protocols.
	name, protos, ok, err := st.ResolveWorkerToken(ctx, token)
	if err != nil || !ok {
		t.Fatalf("resolve valid: ok=%v err=%v", ok, err)
	}
	if name != "home-vps" {
		t.Fatalf("resolved name = %q, want home-vps", name)
	}
	if len(protos) != 2 || protos[0] != "vless" || protos[1] != "trojan" {
		t.Fatalf("resolved protocols = %v, want [vless trojan]", protos)
	}
	if _, _, ok, _ := st.ResolveWorkerToken(ctx, "wt_bogus"); ok {
		t.Fatal("bogus token resolved")
	}

	// Listing never leaks the secret and reflects the new token + protocols.
	list, err := st.ListWorkerTokens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "home-vps" || !list[0].Enabled || len(list[0].Protocols) != 2 {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Restricting protocols to all (empty) clears the allow-list.
	if _, err := st.SetWorkerTokenProtocols(ctx, list[0].ID, nil); err != nil {
		t.Fatalf("set protocols: %v", err)
	}
	if _, protos, _, _ := st.ResolveWorkerToken(ctx, token); protos != nil {
		t.Fatalf("cleared protocols = %v, want nil", protos)
	}

	// Deleting revokes it: the same secret no longer authenticates.
	deleted, err := st.DeleteWorkerToken(ctx, list[0].ID)
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, _, ok, _ := st.ResolveWorkerToken(ctx, token); ok {
		t.Fatal("revoked token still resolves")
	}
}

func TestWorkerTokenRejectsDuplicateAndBadName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.CreateWorkerToken(ctx, "dup", nil); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := st.CreateWorkerToken(ctx, "dup", nil); !errors.Is(err, store.ErrWorkerNameTaken) {
		t.Fatalf("want ErrWorkerNameTaken, got %v", err)
	}
	if _, err := st.CreateWorkerToken(ctx, "bad name!", nil); err == nil {
		t.Fatal("expected invalid-name error")
	}
}
