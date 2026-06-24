// Package kbengine embeds the built frontend so a single binary can serve the
// dashboard. Rebuild the assets with `just web` after changing frontend/.
package kbengine

import (
	"io/fs"

	"embed"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

// Frontend returns the embedded built frontend rooted at the dist directory.
func Frontend() (fs.FS, error) {
	return fs.Sub(frontendFS, "frontend/dist")
}
