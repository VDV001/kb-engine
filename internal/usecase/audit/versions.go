package audit

import (
	"errors"
	"fmt"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoArtefactVersions is returned when the version-drift audit runs without a
// provider. It is an error rather than an empty result: a check that never
// looked must not read as a clean one.
var ErrNoArtefactVersions = errors.New("no artefact version provider configured")

// ArtefactVersions reads the version an artefact declares inside its own file.
// The port is declared here, in the consumer, and implemented by an adapter.
type ArtefactVersions interface {
	// VersionOf returns the version declared in the artefact at the given
	// catalog-relative path. ok is false when the file declares none, which is
	// normal for drafts that keep their version in the filename.
	VersionOf(file string) (domain.Version, bool, error)
}

// WithArtefactVersions wires the provider used by VersionDriftIssues.
func (s *Service) WithArtefactVersions(v ArtefactVersions) *Service {
	s.artefactVersions = v
	return s
}

// VersionDriftIssues reports own artefacts whose catalog version disagrees with
// the version inside the artefact file. The file is the source of truth; the
// catalog holds a copy, and a copy nobody compares goes stale unnoticed.
func (s *Service) VersionDriftIssues() ([]Finding, error) {
	if s.artefactVersions == nil {
		return nil, ErrNoArtefactVersions
	}
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, e := range c.Entries() {
		catalogVersion := e.Version()
		if catalogVersion == nil || !e.IsOwnArtefact() || e.NotesFile() == "" {
			continue
		}
		fileVersion, ok, err := s.artefactVersions.VersionOf(e.NotesFile())
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", e.ID(), err)
		}
		if !ok {
			continue
		}
		switch catalogVersion.Compare(fileVersion) {
		case 0:
			continue
		case -1:
			findings = append(findings, driftFinding(e,
				fmt.Sprintf("версия в каталоге %s отстала от файла %s (%s)",
					catalogVersion, fileVersion, e.NotesFile())))
		default:
			findings = append(findings, driftFinding(e,
				fmt.Sprintf("версия в каталоге %s опережает файл %s (%s) — правку внесли мимо артефакта",
					catalogVersion, fileVersion, e.NotesFile())))
		}
	}
	return findings, nil
}

func driftFinding(e domain.Entry, reason string) Finding {
	return Finding{
		EntryID: e.ID(),
		Title:   e.Title(),
		Current: e.Lifecycle().String(),
		Reasons: []string{reason},
	}
}
