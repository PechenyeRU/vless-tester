// Package webui embeds the built SvelteKit admin dashboard and serves it as a
// static SPA. The coordinator mounts Handler() at "/", below the "/api/v1/"
// control and admin planes, so the dashboard ships inside the single
// coordinator binary (no separate static host, no CORS).
//
// The build output lives in web/build, produced by `make web` (npm run build).
// A committed build/.gitkeep placeholder keeps `go build` working before the
// SPA is built; in that state Handler() serves 404s until a real build exists.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:build
var buildFS embed.FS

// Handler serves the embedded SPA: real files are served directly, and any other
// path falls back to index.html so client-side routing handles deep links and
// refreshes.
func Handler() http.Handler {
	sub, err := fs.Sub(buildFS, "build")
	if err != nil {
		panic(err) // the build directory is always embedded
	}
	fileServer := http.FileServer(http.FS(sub))
	index, _ := fs.ReadFile(sub, "index.html") // nil before the SPA is built

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		// Serve a real asset when one exists at this path.
		if f, err := sub.Open(p); err == nil {
			info, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: hand the route to index.html for client-side routing.
		if index == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
