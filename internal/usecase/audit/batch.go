package audit

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// BatchConsistencyIssues reports entries that disagree with their import batch.
//
// The batch is stored on every entry instead of in a table of its own — a
// deliberate denormalization recorded in ADR-0002. The price of that choice is
// that nothing structurally prevents two entries of one batch from claiming
// different source dates; this check is what pays it.
//
// The majority value of a field within a batch is taken as the batch's value,
// and only the entries that differ are reported. Flagging the whole batch would
// turn one typo into 141 findings.
func (s *Service) BatchConsistencyIssues() ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}

	batches := make(map[int][]domain.Entry)
	for _, e := range c.Entries() {
		if b := e.SourceBatch(); b != nil {
			batches[*b] = append(batches[*b], e)
		}
	}

	var findings []Finding
	for _, batch := range slices.Sorted(maps.Keys(batches)) {
		findings = append(findings, batchDrift(batch, batches[batch])...)
	}
	return findings, nil
}

// batchField is one denormalized field: its name and how to read it as a
// comparable string. Absence reads as "", which is a value like any other — a
// field empty across the whole batch is consistent, a hole where the rest of
// the batch has a value is not.
type batchField struct {
	name string
	of   func(domain.Entry) string
}

var batchFields = []batchField{
	{name: "source", of: func(e domain.Entry) string { return e.Source() }},
	{name: "source_date", of: func(e domain.Entry) string { return day(e.SourceDate()) }},
	{name: "date_added", of: func(e domain.Entry) string { return day(e.DateAdded()) }},
}

func day(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// batchDrift compares every entry of one batch against the batch's majority.
func batchDrift(batch int, entries []domain.Entry) []Finding {
	reasons := make(map[int][]string)
	for _, f := range batchFields {
		expected, ok := majority(entries, f.of)
		if !ok {
			continue
		}
		for _, e := range entries {
			got := f.of(e)
			if got == expected {
				continue
			}
			reasons[e.ID()] = append(reasons[e.ID()],
				fmt.Sprintf("партия %d: %s = %s, у остальных %s",
					batch, f.name, quoteOrEmpty(got), quoteOrEmpty(expected)))
		}
	}

	var findings []Finding
	for _, e := range entries {
		if r, ok := reasons[e.ID()]; ok {
			findings = append(findings, Finding{
				EntryID: e.ID(),
				Title:   e.Title(),
				Current: e.Lifecycle().String(),
				Reasons: r,
			})
		}
	}
	return findings
}

// majority returns the most common value of a field within the batch. ok is
// false when the batch has a single entry: one value cannot disagree with
// itself, and calling it the batch's norm would be a claim with no evidence.
func majority(entries []domain.Entry, of func(domain.Entry) string) (string, bool) {
	if len(entries) < 2 {
		return "", false
	}
	counts := make(map[string]int, len(entries))
	for _, e := range entries {
		counts[of(e)]++
	}
	best, bestCount := "", 0
	for _, v := range slices.Sorted(maps.Keys(counts)) { // deterministic on ties
		if counts[v] > bestCount {
			best, bestCount = v, counts[v]
		}
	}
	return best, true
}

func quoteOrEmpty(s string) string {
	if s == "" {
		return "пусто"
	}
	return fmt.Sprintf("%q", s)
}
