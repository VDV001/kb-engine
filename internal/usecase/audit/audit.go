// Package audit finds entries that are candidates for a lifecycle change.
package audit

import (
	"regexp"

	"github.com/daniil/kb-engine/internal/domain"
)

// CatalogLoader is the port the audit depends on to obtain the catalog. The
// concrete implementation is wired by the caller (Dependency Inversion).
type CatalogLoader interface {
	Load() (*domain.Catalog, error)
}

// Finding is a single audit candidate.
type Finding struct {
	EntryID int
	Title   string
	Current string // current lifecycle
	Reasons []string
}

// Service runs audits over a loaded catalog.
type Service struct {
	loader CatalogLoader
}

// NewService returns a Service backed by loader.
func NewService(loader CatalogLoader) *Service {
	return &Service{loader: loader}
}

// outdatedKeywords are whole-word signals that an entry is stale. They are
// matched on word boundaries (not as substrings) so that, e.g., "удалёнка"
// (remote work) does not trigger on "удалён" (removed).
var outdatedKeywords = []string{
	"снят", "снята", "снято",
	"удалён", "удалена", "удалено",
	"403", "материал был",
	"removed", "no longer available", "deprecated",
}

// outdatedPatterns is outdatedKeywords compiled with Unicode word boundaries.
// (\b is ASCII-only in Go's regexp, so boundaries are spelled out with \p{L}.)
var outdatedPatterns = compileWordPatterns(outdatedKeywords)

func compileWordPatterns(words []string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, len(words))
	for i, w := range words {
		patterns[i] = regexp.MustCompile(`(?i)(^|[^\p{L}])` + regexp.QuoteMeta(w) + `([^\p{L}]|$)`)
	}
	return patterns
}

// OutdatedCandidates returns entries that look outdated but are not yet marked
// outdated: a title/description keyword hit, or a skip-unavailable verdict.
func (s *Service) OutdatedCandidates() ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, e := range c.Entries() {
		if e.Lifecycle().IsOutdated() {
			continue
		}
		if reasons := outdatedReasons(e); len(reasons) > 0 {
			findings = append(findings, Finding{
				EntryID: e.ID(),
				Title:   e.Title(),
				Current: e.Lifecycle().String(),
				Reasons: reasons,
			})
		}
	}
	return findings, nil
}

func outdatedReasons(e domain.Entry) []string {
	var reasons []string
	haystack := e.Title() + " " + e.Description()
	for i, p := range outdatedPatterns {
		if p.MatchString(haystack) {
			reasons = append(reasons, "keyword:"+outdatedKeywords[i])
		}
	}
	if v := e.Verdict(); v != nil && v.IsSkipUnavailable() {
		reasons = append(reasons, "verdict:skip-unavailable")
	}
	return reasons
}
