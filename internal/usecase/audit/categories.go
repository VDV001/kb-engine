package audit

import (
	"errors"
	"fmt"
	"slices"
)

// ErrNoDeclaredCategories is returned when the catalog declares no categories
// at all. Without a dictionary every category would look invented, and a check
// that cannot answer must say so rather than report a clean sheet — the same
// rule the version-drift and missing-file audits follow.
var ErrNoDeclaredCategories = errors.New("catalog declares no categories")

// UndeclaredCategoryIssues reports entries whose category is not a key of
// meta.categories.
//
// The category dictionary is where human names come from, so a category outside
// it is not merely untidy: filters still show the entry, About still draws a box
// for it, and the box gets a technical label because nothing describes it. The
// engine let this happen from either direction — neither `add` nor `inbox`
// checks the value against the dictionary.
//
// Same genre as `integrity` (a link to a missing entry) and `files` (a write-up
// that is not on disk): a value pointing at a declaration that does not exist.
// Cheap — one pass over entries and a map lookup — so it belongs in `all`,
// unlike the link check, which asks the network.
func (s *Service) UndeclaredCategoryIssues() ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	declared := c.CategoryLabels()
	if len(declared) == 0 {
		return nil, ErrNoDeclaredCategories
	}

	var findings []Finding
	for _, e := range c.Entries() {
		cat := e.Category().String()
		if _, ok := declared[cat]; ok {
			continue
		}
		// Категория названа в причине: без неё непонятно, что чинить —
		// переписать категорию записи или дописать словарь.
		findings = append(findings, driftFinding(e,
			fmt.Sprintf("категория %q не объявлена в meta.categories", cat)))
	}
	return findings, nil
}

// UnusedCategories returns declared categories no entry uses, sorted.
//
// This is the other side of the same question and deliberately NOT a finding:
// a category the base declares and has not filled yet is legitimate, and a
// finding per empty section would train the reader to skim past the ones that
// matter. The caller decides how to show it; the CLI prints one line.
func (s *Service) UnusedCategories() ([]string, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	declared := c.CategoryLabels()
	if len(declared) == 0 {
		return nil, ErrNoDeclaredCategories
	}

	used := make(map[string]struct{}, len(declared))
	for _, e := range c.Entries() {
		used[e.Category().String()] = struct{}{}
	}

	var out []string
	for cat := range declared {
		if _, ok := used[cat]; !ok {
			out = append(out, cat)
		}
	}
	slices.Sort(out)
	return out, nil
}
