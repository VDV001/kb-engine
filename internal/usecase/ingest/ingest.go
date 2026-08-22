// Package ingest is the use case for adding new entries to a catalog. It owns
// the application policy — deduplicate by URL, allocate fresh ids, stamp the
// date added — while delegating validation to the domain. It is pure: it takes
// an already-loaded catalog and id-less params, and returns the entries to
// persist plus a report. I/O (loading and writing the catalog) is the caller's
// concern.
package ingest

import (
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Report summarizes the outcome of a Plan run.
type Report struct {
	Added            int
	SkippedDuplicate int // URL already present (in catalog or earlier in the batch)
	SkippedNoURL     int // entry carried no URL to dedup on
	// CategoriesUnchecked is true when the catalog declares no categories at
	// all, so the category of every added entry went unverified. Silence would
	// be indistinguishable from a check that found nothing.
	CategoriesUnchecked bool
}

// Plan filters params against the catalog and returns the entries to add. An
// entry is skipped when it has no URL, or when its URL already exists in the
// catalog or earlier in the same batch. Surviving params get a sequential id
// (continuing from the catalog's NextID) and the date added set to now. A param
// that violates an entry invariant aborts the whole plan with an error.
func Plan(c *domain.Catalog, params []domain.EntryParams, now time.Time) ([]domain.Entry, Report, error) {
	seen := existingURLs(c)
	declared := c.CategoryLabels()
	dateAdded := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	nextID := c.NextID()

	var added []domain.Entry
	var rep Report
	rep.CategoriesUnchecked = len(declared) == 0
	for _, p := range params {
		if err := checkCategory(declared, p); err != nil {
			return nil, Report{}, err
		}
		url := strings.TrimSpace(p.URL)
		if url == "" {
			rep.SkippedNoURL++
			continue
		}
		if _, dup := seen[url]; dup {
			rep.SkippedDuplicate++
			continue
		}

		p.ID = nextID
		p.DateAdded = &dateAdded
		e, err := domain.NewEntry(p)
		if err != nil {
			return nil, Report{}, fmt.Errorf("ingest entry %q: %w", p.Title, err)
		}

		seen[url] = struct{}{}
		added = append(added, e)
		nextID++
		rep.Added++
	}
	return added, rep, nil
}

// existingURLs collects the non-empty URLs already present in the catalog.
func existingURLs(c *domain.Catalog) map[string]struct{} {
	set := make(map[string]struct{})
	for _, e := range c.Entries() {
		if u := strings.TrimSpace(e.URL()); u != "" {
			set[u] = struct{}{}
		}
	}
	return set
}
