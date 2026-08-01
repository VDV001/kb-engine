package domain_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// IsOwnArtefact separates what the owner writes and versions himself from
// material collected from elsewhere. It decides which of the two version fields
// an entry may carry, so it is a domain rule, not a migration detail.
//
// The rule is category-or-path, checked against the live catalog: on all 1340
// entries it agrees with the wider rule that also consults the legacy "type"
// field, so that field adds nothing here.
func TestEntryIsOwnArtefact(t *testing.T) {
	cases := []struct {
		name     string
		category string
		file     string
		want     bool
	}{
		{name: "standards category", category: "standards", want: true},
		{name: "creations category", category: "creations", want: true},
		{name: "standard by path", category: "ai-agents-tools", file: "standards/kb-as-product/v1.md", want: true},
		{name: "article draft by path", category: "ai-agents-tools", file: "creations/habr/2026-04-28_brain-fry/v5-final.md", want: true},
		{name: "deepread by path", category: "knowledge-management", file: "docs/2026-05-07_claude-config-as-code-deepread_v1.md", want: true},
		{name: "someone else's article", category: "ai-agents-tools"},
		{name: "note file outside the owner's trees", category: "ai-agents-tools", file: "notes/2026-05-01_x.md"},
		{name: "path that merely mentions docs", category: "ai-agents-tools", file: "vendor/docs/x.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validArticle(t)
			cat, err := domain.NewCategory(c.category)
			if err != nil {
				t.Fatalf("setup category %q: %v", c.category, err)
			}
			p.Category = cat
			p.NotesFile = c.file

			e, err := domain.NewEntry(p)
			if err != nil {
				t.Fatalf("NewEntry: %v", err)
			}
			if got := e.IsOwnArtefact(); got != c.want {
				t.Fatalf("IsOwnArtefact() = %v, want %v (category %q, file %q)", got, c.want, c.category, c.file)
			}
		})
	}
}
