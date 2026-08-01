package ingest

import (
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// PlanArtefacts is Plan for the owner's own material: a standard, a write-up, a
// draft. Such an entry has no address on the internet — it has a file in the
// knowledge base, and that file is its identity.
//
// The difference from Plan is deliberate and small: identity is the notes file
// rather than the url, and a param without one is an error rather than a silent
// skip. Skipping is right for a bot inbox, where a link without a url is noise
// in someone else's export; here the caller typed the entry by hand, and
// dropping it would report success having added nothing.
func PlanArtefacts(c *domain.Catalog, params []domain.EntryParams, now time.Time) ([]domain.Entry, Report, error) {
	seen := existingFiles(c)
	dateAdded := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	nextID := c.NextID()

	var added []domain.Entry
	var rep Report
	for _, p := range params {
		file := strings.TrimSpace(p.NotesFile)
		if file == "" {
			return nil, Report{}, fmt.Errorf("artefact %q has no file — an own artefact is identified by the file it lives in", p.Title)
		}
		if _, dup := seen[file]; dup {
			rep.SkippedDuplicate++
			continue
		}

		p.ID = nextID
		p.DateAdded = &dateAdded
		e, err := domain.NewEntry(p)
		if err != nil {
			return nil, Report{}, fmt.Errorf("artefact %q: %w", p.Title, err)
		}

		seen[file] = struct{}{}
		added = append(added, e)
		nextID++
		rep.Added++
	}
	return added, rep, nil
}

// existingFiles collects the non-empty notes files already present.
func existingFiles(c *domain.Catalog) map[string]struct{} {
	files := make(map[string]struct{})
	for _, e := range c.Entries() {
		if f := strings.TrimSpace(e.NotesFile()); f != "" {
			files[f] = struct{}{}
		}
	}
	return files
}
