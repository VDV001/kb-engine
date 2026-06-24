// Package audit finds entries that are candidates for a lifecycle change.
package audit

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// canonicalReferenceThreshold is how many other entries must reference an entry
// (via related_ids) before it is suggested for canonical status.
const canonicalReferenceThreshold = 3

// ageMonthsThreshold is how old a Habr article may get before it is suggested
// for an outdated-lifecycle review (Habr links drift faster than evergreen
// content).
const ageMonthsThreshold = 18

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

// AgeCandidates returns Habr articles older than ageMonthsThreshold that are
// not already marked outdated — candidates for an outdated-lifecycle review.
// now is supplied by the caller so the audit is deterministic and testable.
func (s *Service) AgeCandidates(now time.Time) ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	cutoff := now.AddDate(0, -ageMonthsThreshold, 0)
	var findings []Finding
	for _, e := range c.Entries() {
		if e.Lifecycle().IsOutdated() || !isHabr(e) {
			continue
		}
		created := e.DateCreated()
		if created == nil || !created.Before(cutoff) {
			continue
		}
		findings = append(findings, Finding{
			EntryID: e.ID(),
			Title:   e.Title(),
			Current: e.Lifecycle().String(),
			Reasons: []string{fmt.Sprintf("habr article older than %d months (created %s)",
				ageMonthsThreshold, created.Format("2006-01-02"))},
		})
	}
	return findings, nil
}

// isHabr reports whether an entry points at a Habr article, by URL or habr_id.
func isHabr(e domain.Entry) bool {
	return e.HabrID() != nil || strings.Contains(e.URL(), "habr.com")
}

// CanonicalHealthIssues returns canonical entries missing the context a
// canonical reference should carry: a description, notes, or related_ids. (An
// entry's lifecycle is always set by construction, so the legacy "lifecycle
// consistency" check is enforced by the domain, not audited here.)
func (s *Service) CanonicalHealthIssues() ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, e := range c.Entries() {
		if !e.Lifecycle().IsCanonical() {
			continue
		}
		var reasons []string
		if strings.TrimSpace(e.Description()) == "" {
			reasons = append(reasons, "canonical entry missing description")
		}
		if strings.TrimSpace(e.Notes()) == "" {
			reasons = append(reasons, "canonical entry missing notes")
		}
		if len(e.RelatedIDs()) == 0 {
			reasons = append(reasons, "canonical entry has no related_ids")
		}
		if len(reasons) > 0 {
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

// CanonicalCandidates returns entries referenced by at least
// canonicalReferenceThreshold other entries that are not already canonical.
func (s *Service) CanonicalCandidates() ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return canonicalCandidates(c), nil
}

func canonicalCandidates(c *domain.Catalog) []Finding {
	refCount := make(map[int]int)
	for _, e := range c.Entries() {
		for _, rid := range e.RelatedIDs() {
			refCount[rid]++
		}
	}
	var findings []Finding
	for _, e := range c.Entries() {
		if e.Lifecycle().IsCanonical() {
			continue
		}
		if n := refCount[e.ID()]; n >= canonicalReferenceThreshold {
			findings = append(findings, Finding{
				EntryID: e.ID(),
				Title:   e.Title(),
				Current: e.Lifecycle().String(),
				Reasons: []string{fmt.Sprintf("referenced by %d entries", n)},
			})
		}
	}
	return findings
}

// SupersessionIssues returns entries whose supersedes_id is dangling (target
// missing) or part of a cycle.
func (s *Service) SupersessionIssues() ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return supersessionIssues(c), nil
}

func supersessionIssues(c *domain.Catalog) []Finding {
	byID := make(map[int]domain.Entry)
	for _, e := range c.Entries() {
		byID[e.ID()] = e
	}

	var findings []Finding
	for _, e := range c.Entries() {
		sup := e.SupersedesID()
		if sup == nil {
			continue
		}
		var reason string
		switch {
		case !exists(byID, *sup):
			reason = fmt.Sprintf("supersedes_id %d does not exist", *sup)
		case supersedesCycle(e.ID(), byID):
			reason = "supersedes_id forms a cycle"
		}
		if reason != "" {
			findings = append(findings, Finding{
				EntryID: e.ID(),
				Title:   e.Title(),
				Current: e.Lifecycle().String(),
				Reasons: []string{reason},
			})
		}
	}
	return findings
}

func exists(byID map[int]domain.Entry, id int) bool {
	_, ok := byID[id]
	return ok
}

// supersedesCycle reports whether following supersedes_id from start eventually
// loops back into a node already on the path.
func supersedesCycle(start int, byID map[int]domain.Entry) bool {
	visited := make(map[int]bool)
	cur := start
	for {
		e, ok := byID[cur]
		if !ok {
			return false
		}
		sup := e.SupersedesID()
		if sup == nil {
			return false
		}
		next := *sup
		if next == start || visited[next] {
			return true
		}
		visited[next] = true
		cur = next
	}
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
