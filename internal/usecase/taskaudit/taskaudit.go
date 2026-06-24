// Package taskaudit implements the ADR-015 hygiene check: a task carrying a
// Habr article id must not be marked completed unless the catalog actually has
// that article. It is pure — given a loaded catalog and the parsed task list it
// classifies the tasks; parsing the task list and loading the catalog are the
// caller's concern.
package taskaudit

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Task is one item from the task list, reduced to what the audit needs. HabrID
// is the article id referenced in the task ("" when the task references none).
type Task struct {
	ID     string
	Status string
	HabrID string
}

// Result groups the tasks by how they relate to the catalog.
type Result struct {
	// Orphans are completed tasks whose Habr id is absent from the catalog —
	// the ADR-015 violation (marked done before the entry exists).
	Orphans []Task
	// PendingPresent are not-yet-completed tasks whose article is already in
	// the catalog and could therefore be closed.
	PendingPresent []Task
	// Consistent are completed tasks whose article is present — the healthy case.
	Consistent []Task
}

// HasOrphans reports whether the audit found any ADR-015 violation.
func (r Result) HasOrphans() bool { return len(r.Orphans) > 0 }

var reArticleID = regexp.MustCompile(`/articles/(\d+)`)

// Audit classifies tasks against the catalog. Tasks without a Habr id are
// ignored. Status matching is case-insensitive.
func Audit(c *domain.Catalog, tasks []Task) Result {
	present := presentHabrIDs(c)
	var res Result
	for _, t := range tasks {
		if t.HabrID == "" {
			continue
		}
		_, inCatalog := present[t.HabrID]
		switch {
		case strings.EqualFold(t.Status, "completed") && inCatalog:
			res.Consistent = append(res.Consistent, t)
		case strings.EqualFold(t.Status, "completed"):
			res.Orphans = append(res.Orphans, t)
		case inCatalog:
			res.PendingPresent = append(res.PendingPresent, t)
		}
	}
	return res
}

// presentHabrIDs returns the set of Habr article ids the catalog covers, taken
// from both the URL (/articles/NNN, including company paths) and the explicit
// habr_id field — the union is more robust than either signal alone.
func presentHabrIDs(c *domain.Catalog) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, e := range c.Entries() {
		if m := reArticleID.FindStringSubmatch(e.URL()); m != nil {
			ids[m[1]] = struct{}{}
		}
		if h := e.HabrID(); h != nil {
			ids[strconv.Itoa(*h)] = struct{}{}
		}
	}
	return ids
}
