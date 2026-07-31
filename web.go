// Package kbengine embeds the built frontend so a single binary can serve the
// dashboard. The bundle is a build artifact and is not in the repository: run
// `just web` once after cloning, and again after changing frontend/. Without it
// this package does not compile, which is deliberate — a binary that reports
// success while serving no dashboard would be worse than one that fails to build.
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
