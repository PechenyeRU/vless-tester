// This file is the public subscription distribution endpoint. Unlike the worker
// and admin planes, it is intentionally unauthenticated: it is the URL proxy
// clients point at to fetch the working list (like subs-check's :8199/sub). It
// only ever serves artifacts rendered from public share URIs, so it exposes no
// inner-working (no worker, vantage or diagnostic data).
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/whitedns/vless-tester/internal/convert"
	"github.com/whitedns/vless-tester/internal/store"
)

// SubStore is the data layer the subscription endpoint needs. The real
// *store.Store satisfies it; tests inject a fake.
type SubStore interface {
	PublishedArtifact(ctx context.Context, target string) (store.PublishedArtifact, error)
	// SubPath returns the optional obfuscated path token: when set, /sub is only
	// reachable at /sub/<token> and bare /sub is hidden.
	SubPath(ctx context.Context) (string, error)
}

// SubServer serves the public GET /sub endpoint.
type SubServer struct {
	Store SubStore
	Logf  func(format string, args ...any)
}

func (s *SubServer) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Handler builds the routed http.Handler for the subscription endpoint. Both the
// bare /sub and the path-token /sub/{token} forms route here; which one is valid
// depends on the sub.path setting (checked per request).
func (s *SubServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sub", s.handleSub)
	mux.HandleFunc("GET /sub/{token}", s.handleSub)
	return mux
}

// handleSub serves the latest rendered artifact for ?target= (default base64).
func (s *SubServer) handleSub(w http.ResponseWriter, r *http.Request) {
	// Obfuscated-path gate: when sub.path is set, only /sub/<token> works and bare
	// /sub is hidden; when unset, only bare /sub works.
	want, err := s.Store.SubPath(r.Context())
	if err != nil {
		s.logf("sub: path setting: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if r.PathValue("token") != want {
		http.NotFound(w, r)
		return
	}

	target := r.URL.Query().Get("target")
	if target == "" {
		target = convert.TargetBase64
	}
	if !convert.Supported(target) {
		http.Error(w, "unknown target; supported: "+strings.Join(convert.Targets, ", "), http.StatusBadRequest)
		return
	}

	art, err := s.Store.PublishedArtifact(r.Context(), target)
	switch {
	case errors.Is(err, store.ErrNoArtifact):
		http.Error(w, "no subscription published yet", http.StatusNotFound)
		return
	case err != nil:
		s.logf("sub: serve %s: %v", target, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", art.ContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(art.Content); err != nil {
		s.logf("sub: write %s: %v", target, err)
	}
}
