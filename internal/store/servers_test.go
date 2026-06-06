package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/whitedns/vless-tester/internal/store"
)

func TestUpdateServer(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, err := st.UpsertServer(ctx, sampleServer(1))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	edited := sampleServer(1)
	edited.Fingerprint = "fp-edited"
	edited.Host = "newhost"
	edited.RawURI = "vless://uuid@newhost:8443"
	edited.Country = "CH"
	edited.SeqName = "CH1"
	ok, err := st.UpdateServer(ctx, id, edited)
	if err != nil || !ok {
		t.Fatalf("update: ok=%v err=%v", ok, err)
	}

	got, err := st.GetServer(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Host != "newhost" || got.Fingerprint != "fp-edited" || got.Country != "CH" || got.SeqName != "CH1" {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestUpdateServerConflict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	st.UpsertServer(ctx, sampleServer(1))
	id2, _ := st.UpsertServer(ctx, sampleServer(2))

	// Re-pointing server 2 onto server 1's fingerprint collides.
	clash := sampleServer(2)
	clash.Fingerprint = "fp-1"
	if _, err := st.UpdateServer(ctx, id2, clash); !errors.Is(err, store.ErrServerExists) {
		t.Fatalf("want ErrServerExists, got %v", err)
	}
}

func TestUpdateServerNotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	ok, err := st.UpdateServer(ctx, 9999, sampleServer(1))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatal("want ok=false for a missing server")
	}
}

func TestDeleteServerCascades(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	id, _ := st.UpsertServer(ctx, sampleServer(1))
	seedRun(t, st, "w1", id, 100, 5) // a dependent test_run

	ok, err := st.DeleteServer(ctx, id)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if _, err := st.GetServer(ctx, id); err == nil {
		t.Fatal("expected the server to be gone")
	}

	// Deleting again reports no row matched.
	if ok, _ := st.DeleteServer(ctx, id); ok {
		t.Fatal("want ok=false deleting a missing server")
	}
}
