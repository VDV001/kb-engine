package audit

import (
	"fmt"
	"time"
)

// linkCheckStaleAfter is how long a link check stays trustworthy. Two months is
// short enough to catch a withdrawal while the entry still matters, and long
// enough that a monthly scan keeps the list empty.
const linkCheckStaleAfter = 2 * 30 * 24 * time.Hour

// UncheckedLinkIssues reports entries whose url the base has never asked about,
// or asked long ago.
//
// This is the audit that answers «what does the base not know about itself».
// On the live catalog 527 entries with a url had never been checked, and no
// screen said so — which is how a knowledge base accumulates links nobody knows
// are still real.
func (s *Service) UncheckedLinkIssues(now time.Time) ([]Finding, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, e := range c.Entries() {
		if e.URL() == "" {
			continue // nothing to check
		}
		checked := e.DriftCheckDate()
		switch {
		case checked == nil:
			findings = append(findings, Finding{
				EntryID: e.ID(), Title: e.Title(), Current: e.Lifecycle().String(),
				Reasons: []string{"ссылка не проверялась ни разу"},
			})
		case now.Sub(*checked) > linkCheckStaleAfter:
			findings = append(findings, Finding{
				EntryID: e.ID(), Title: e.Title(), Current: e.Lifecycle().String(),
				Reasons: []string{fmt.Sprintf("ссылка проверялась %s — больше двух месяцев назад",
					checked.Format("2006-01-02"))},
			})
		}
	}
	return findings, nil
}
