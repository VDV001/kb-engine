// Package audit finds entries that are candidates for a lifecycle change.
package audit

import (
	"strings"

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

var outdatedKeywords = []string{
	"снят", "удал", "403", "материал был",
	"removed", "no longer available", "deprecated",
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
	haystack := strings.ToLower(e.Title() + " " + e.Description())
	for _, kw := range outdatedKeywords {
		if strings.Contains(haystack, kw) {
			reasons = append(reasons, "keyword:"+kw)
		}
	}
	if v := e.Verdict(); v != nil && v.IsSkipUnavailable() {
		reasons = append(reasons, "verdict:skip-unavailable")
	}
	return reasons
}
