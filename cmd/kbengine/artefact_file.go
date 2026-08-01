package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// artefactRoot is the knowledge base the catalog's file paths are relative to.
// Derived from the catalog path — _data/catalog.json sits one level inside the
// base — which is how the version audit already resolves artefacts. Deriving it
// twice by different rules is how the two would eventually disagree.
func artefactRoot(catalogPath string) string {
	return filepath.Dir(filepath.Dir(catalogPath))
}

// checkArtefactExists refuses a path that points at nothing.
//
// An empty path means the flag was not passed and is not this function's
// business. A path that names a file which is not there is: twelve entries
// carried exactly that — a write-up renamed or never written — and nothing
// said so until a hand-written script went looking. The catalog cannot tell a
// path it has never checked from a path it has checked and found good, so the
// check belongs at the moment the value is written.
//
// The message names the absolute path that was looked at, not the relative one
// that was typed: the usual cause is running the command from somewhere other
// than the knowledge base, and only the resolved path shows that.
// The form of the path is checked first, by the domain: "../../etc/passwd"
// leaves the knowledge base, and saying it does not exist would both hide the
// real objection and answer a question about a file outside the base.
func checkArtefactExists(catalogPath, file string) error {
	if strings.TrimSpace(file) == "" {
		return nil
	}
	p, err := domain.NewNotesPath(file)
	if err != nil {
		return err
	}
	full := filepath.Join(artefactRoot(catalogPath), filepath.FromSlash(p.String()))
	switch _, err := os.Stat(full); {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s does not exist (looked at %s)", p, full)
	default:
		return err
	}
}
