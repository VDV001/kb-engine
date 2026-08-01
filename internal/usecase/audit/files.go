package audit

import (
	"errors"
	"fmt"
)

// ErrNoArtefactFiles is returned when the missing-file audit runs without a
// provider. An audit that never looked must not read as a clean one — the same
// rule the version drift check follows.
var ErrNoArtefactFiles = errors.New("no artefact file provider configured")

// ArtefactFiles answers whether the write-up an entry points at is on disk.
// Declared here, in the consumer; implemented by an adapter.
//
// Separate from ArtefactVersions on purpose: "does this file exist" and "what
// version does it declare" are different questions, and the version reader
// answers the second by treating a missing file as "declares none" — which is
// exactly the case this audit is looking for.
type ArtefactFiles interface {
	Exists(file string) (bool, error)
}

// WithArtefactFiles wires the provider used by MissingFileIssues.
func (s *Service) WithArtefactFiles(f ArtefactFiles) *Service {
	s.artefactFiles = f
	return s
}

// MissingFileIssues reports entries whose write-up is not on disk.
//
// Twelve entries carried such a path — renamed directories, a prefix dropped —
// and nothing in the engine could say so: the catalog cannot tell a path it has
// never checked from one it has checked and found good. Entries that carry no
// file at all are not findings: having no write-up is a different state from
// pointing at one that is gone.
func (s *Service) MissingFileIssues() ([]Finding, error) {
	if s.artefactFiles == nil {
		return nil, ErrNoArtefactFiles
	}
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, e := range c.Entries() {
		file := e.NotesFile()
		if file == "" {
			continue
		}
		ok, err := s.artefactFiles.Exists(file)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", e.ID(), err)
		}
		if !ok {
			findings = append(findings, driftFinding(e, fmt.Sprintf("файла нет на диске: %s", file)))
		}
	}
	return findings, nil
}
