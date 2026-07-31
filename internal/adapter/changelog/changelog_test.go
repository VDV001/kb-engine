package changelog_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/changelog"
)

const sample = `# Changelog

All notable changes.

## [Unreleased]
### Added
- work in progress feature

## [1.5.0] — 2026-05-02
> Большой релиз про память
### Added
- feature A
- feature B
### Changed
- behaviour X
  with a continuation line

## [1.4.0] - 2026-04-01
### Fixed
- bug Y
`

func TestParse(t *testing.T) {
	doc := changelog.Parse(sample)

	if len(doc.Releases) != 3 {
		t.Fatalf("releases = %d, want 3 (Unreleased + 2)", len(doc.Releases))
	}

	// current_* point at the latest RELEASED version, not Unreleased.
	if doc.CurrentVersion != "1.5.0" || doc.CurrentDate != "2026-05-02" {
		t.Errorf("current = %s/%s, want 1.5.0/2026-05-02", doc.CurrentVersion, doc.CurrentDate)
	}
	if doc.CurrentTagline != "Большой релиз про память" {
		t.Errorf("current tagline = %q", doc.CurrentTagline)
	}

	unrel := doc.Releases[0]
	if unrel.Version != "Unreleased" || unrel.Date != "" {
		t.Errorf("release[0] = %s/%s, want Unreleased/empty", unrel.Version, unrel.Date)
	}

	r15 := doc.Releases[1]
	if r15.Version != "1.5.0" || r15.Date != "2026-05-02" {
		t.Errorf("release[1] = %s/%s", r15.Version, r15.Date)
	}
	if r15.Tagline != "Большой релиз про память" {
		t.Errorf("release[1] tagline = %q", r15.Tagline)
	}
	if len(r15.Sections) != 2 || r15.Sections[0].Name != "Added" || r15.Sections[1].Name != "Changed" {
		t.Fatalf("release[1] sections = %+v, want [Added, Changed] in order", r15.Sections)
	}
	if strings.Join(r15.Sections[0].Items, "|") != "feature A|feature B" {
		t.Errorf("Added items = %v", r15.Sections[0].Items)
	}
	// Continuation line is appended to the preceding bullet.
	if got := r15.Sections[1].Items[0]; got != "behaviour X with a continuation line" {
		t.Errorf("Changed item = %q, want continuation appended", got)
	}

	// Em-dash and hyphen date separators both parse.
	if doc.Releases[2].Version != "1.4.0" || doc.Releases[2].Date != "2026-04-01" {
		t.Errorf("release[2] = %s/%s, want 1.4.0/2026-04-01", doc.Releases[2].Version, doc.Releases[2].Date)
	}
}

func TestParse_DropsEmptyUnreleased(t *testing.T) {
	const in = `## [Unreleased]

## [1.0.0] — 2026-01-01
### Added
- thing
`
	doc := changelog.Parse(in)
	if len(doc.Releases) != 1 || doc.Releases[0].Version != "1.0.0" {
		t.Fatalf("releases = %+v, want only 1.0.0 (empty Unreleased dropped)", doc.Releases)
	}
}

func TestParse_MergesDuplicateSections(t *testing.T) {
	const in = `## [1.0.0] — 2026-01-01
### Added
- first
### Changed
- a change
### Added
- second
`
	doc := changelog.Parse(in)
	if len(doc.Releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(doc.Releases))
	}
	r := doc.Releases[0]
	// The two "Added" blocks must merge into one section (valid JSON object keys).
	added := 0
	for _, s := range r.Sections {
		if s.Name == "Added" {
			added++
			if len(s.Items) != 2 {
				t.Errorf("merged Added items = %v, want [first second]", s.Items)
			}
		}
	}
	if added != 1 {
		t.Errorf("Added section appears %d times, want 1 (merged)", added)
	}
}

func TestParse_NoReleases(t *testing.T) {
	doc := changelog.Parse("# Changelog\n\njust prose, no versions\n")
	if len(doc.Releases) != 0 {
		t.Errorf("releases = %d, want 0", len(doc.Releases))
	}
	if doc.CurrentVersion != "0.0.0" {
		t.Errorf("current version = %q, want 0.0.0 fallback", doc.CurrentVersion)
	}
}

// Аннотация релиза переносится по ширине файла, а склеивать её никто не
// склеивал: в JSON уезжала только первая строка, и все три релиза
// показывались фразой, оборванной на запятой.
func TestParse_MultilineTaglineIsJoined(t *testing.T) {
	doc := changelog.Parse(`# Changelog

## [0.3.0] — 2026-07-31

> The dashboard port lands: every view but Projects now reads
> from the engine, and the graph says something new.

### Added

- A thing.
`)
	if len(doc.Releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(doc.Releases))
	}

	want := "The dashboard port lands: every view but Projects now reads from the engine, and the graph says something new."
	if got := doc.Releases[0].Tagline; got != want {
		t.Errorf("tagline =\n%q\nwant\n%q", got, want)
	}
	// Строки цитаты не должны просочиться в пункты раздела.
	for _, s := range doc.Releases[0].Sections {
		for _, item := range s.Items {
			if strings.Contains(item, "from the engine") {
				t.Errorf("хвост аннотации уехал в пункты раздела: %q", item)
			}
		}
	}
}
