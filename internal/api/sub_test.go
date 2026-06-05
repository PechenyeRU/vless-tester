package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whitedns/vless-tester/internal/convert"
	"github.com/whitedns/vless-tester/internal/store"
)

// fakeSubStore serves canned artifacts; an absent target yields ErrNoArtifact.
type fakeSubStore struct {
	artifacts map[string]store.PublishedArtifact
	path      string
}

func (f *fakeSubStore) PublishedArtifact(_ context.Context, target string) (store.PublishedArtifact, error) {
	a, ok := f.artifacts[target]
	if !ok {
		return store.PublishedArtifact{}, store.ErrNoArtifact
	}
	return a, nil
}

func (f *fakeSubStore) SubPath(_ context.Context) (string, error) { return f.path, nil }

func newSubServer(arts map[string]store.PublishedArtifact) http.Handler {
	return (&SubServer{Store: &fakeSubStore{artifacts: arts}}).Handler()
}

func TestSubServesTarget(t *testing.T) {
	h := newSubServer(map[string]store.PublishedArtifact{
		convert.TargetClash: {Target: "clash", Content: []byte("proxies: []\n"), ContentType: "text/yaml; charset=utf-8"},
	})

	req := httptest.NewRequest(http.MethodGet, "/sub?target=clash", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/yaml; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if rec.Body.String() != "proxies: []\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestSubDefaultsToBase64(t *testing.T) {
	h := newSubServer(map[string]store.PublishedArtifact{
		convert.TargetBase64: {Target: "base64", Content: []byte("Zm9v"), ContentType: "text/plain; charset=utf-8"},
	})

	req := httptest.NewRequest(http.MethodGet, "/sub", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "Zm9v" {
		t.Fatalf("default target: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestSubUnknownTarget(t *testing.T) {
	h := newSubServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/sub?target=bogus", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubObfuscatedPath(t *testing.T) {
	arts := map[string]store.PublishedArtifact{
		convert.TargetBase64: {Content: []byte("Zm9v"), ContentType: "text/plain"},
	}
	h := (&SubServer{Store: &fakeSubStore{artifacts: arts, path: "s3cr3t"}}).Handler()

	// Bare /sub is hidden when a path is configured.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub?target=base64", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bare /sub with path set: want 404, got %d", rec.Code)
	}
	// The token path serves.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/s3cr3t?target=base64", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "Zm9v" {
		t.Fatalf("/sub/<token>: status=%d body=%q", rec.Code, rec.Body.String())
	}
	// A wrong token 404s.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/wrong?target=base64", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("wrong token: want 404, got %d", rec.Code)
	}
}

func TestSubNoPathRejectsToken(t *testing.T) {
	h := newSubServer(map[string]store.PublishedArtifact{
		convert.TargetBase64: {Content: []byte("Zm9v"), ContentType: "text/plain"},
	})
	// With no path configured, the token form is hidden.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/anything?target=base64", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("token form without path: want 404, got %d", rec.Code)
	}
}

func TestSubNotPublishedYet(t *testing.T) {
	h := newSubServer(map[string]store.PublishedArtifact{})
	req := httptest.NewRequest(http.MethodGet, "/sub?target=singbox", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
