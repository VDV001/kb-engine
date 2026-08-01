// Package artefactfs reads the version an artefact declares inside its own
// markdown file. It implements the audit's ArtefactVersions port.
package artefactfs

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// maxHeaderLines bounds how far into a file the version is looked for. The
// front matter sits at the top; scanning a whole document to find nothing is
// work no answer depends on.
const maxHeaderLines = 40

// Reader resolves catalog-relative artefact paths against Root.
type Reader struct {
	Root string
}

// VersionOf returns the version declared in the artefact's front matter.
//
// A missing file is not an error: the catalog outlived several directory
// layouts and points at paths that no longer exist. That is a different defect
// from a stale version, and reporting it here would mix the two.
func (r Reader) VersionOf(file string) (domain.Version, bool, error) {
	if strings.TrimSpace(file) == "" {
		return domain.Version{}, false, nil
	}
	path := filepath.Join(r.Root, filepath.Clean("/"+file))

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.Version{}, false, nil
		}
		return domain.Version{}, false, fmt.Errorf("open artefact %s: %w", file, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for i := 0; i < maxHeaderLines && scanner.Scan(); i++ {
		raw, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "version:")
		if !ok {
			continue
		}
		v, err := domain.NewVersion(strings.Trim(strings.TrimSpace(raw), `"'`))
		if err != nil {
			// A header line that is not a semver says nothing about drift.
			return domain.Version{}, false, nil
		}
		return v, true, nil
	}
	if err := scanner.Err(); err != nil {
		return domain.Version{}, false, fmt.Errorf("read artefact %s: %w", file, err)
	}
	return domain.Version{}, false, nil
}
