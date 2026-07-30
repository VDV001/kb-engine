package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves static files from fsys. Requests that don't map to a real
// file fall back to index.html, so client-side (SPA) routes work on reload.
//
// Caching is stated explicitly rather than left to the browser's heuristics.
// Without a single cache header the browser guesses, and it guessed wrong in
// the way that hurts most: it kept a stale index.html, which points at the
// bundle by content hash, so a rebuilt engine kept serving the previous page
// and the fix looked like it had not been applied at all.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(fsys, name); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			setCache(w, "index.html")
			fileServer.ServeHTTP(w, r)
			return
		}
		setCache(w, name)
		fileServer.ServeHTTP(w, r)
	})
}

// setCache marks hashed build artefacts as immutable and everything else as
// must-revalidate.
//
// Files under assets/ carry a content hash in the name: a changed file gets a
// changed URL, so the old one can be kept forever. index.html carries no hash
// and is the only place where those URLs are written down — it must be checked
// on every load, otherwise a deploy reaches nobody.
func setCache(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}
