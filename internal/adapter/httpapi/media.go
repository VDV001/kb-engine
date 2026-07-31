package httpapi

import (
	"io/fs"
	"net/http"
)

// mediaHandler serves the owner's images — project screenshots for the Projects
// view — from a directory the engine is pointed at.
//
// Deliberately not frontend/public: everything there is compiled into the
// embedded bundle, and the bundle ships in an AGPL-public repo. A screenshot of
// the owner's product is his content, on the same footing as team.json and
// projects.json, so it travels the same way — a file on his disk, a flag, and a
// renderer that knows nothing about what is in it.
//
// Cache-Control is no-cache rather than the immutable used for build assets:
// those carry a content hash in the name, so a change means a new URL, while a
// screenshot keeps its name when the owner replaces it. Immutable here would
// mean the replacement is never seen.
func mediaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
