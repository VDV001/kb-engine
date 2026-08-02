package audit

// Health is everything the health screen shows: what to look at, what looks
// duplicated, and what the last link scan learned.
//
// One call rather than one per check, because the composition — which checks
// add up to "health" — is a decision, and a decision copied into every surface
// is one that drifts. A screen missing a check looks exactly like a screen
// where that check found nothing.
//
// ponytail: the web page still assembles the same thing from three endpoints
// (/api/audits, /api/duplicates, /api/link-health), so the composition lives in
// two places until those are folded into one. Not done here because the API
// contract is what the dashboard is built on, and changing it is a separate
// piece of work from teaching the terminal to show health at all.
type Health struct {
	Outdated     []Finding
	Canonical    []Finding
	Supersession []Finding
	Duplicates   []DuplicateGroup
	Links        LinkHealth
}

// Total counts the findings, so a caller can say "everything is clean" without
// knowing which sections exist. Link health is a summary rather than a list of
// findings and is not counted here.
func (h Health) Total() int {
	return len(h.Outdated) + len(h.Canonical) + len(h.Supersession) + len(h.Duplicates)
}

// Health gathers every check on one read of the catalog.
//
// The file is read once, not once per check: on the live catalog that is the
// difference between one pass over 1400 entries and five.
func (s *Service) Health() (Health, error) {
	c, err := s.loader.Load()
	if err != nil {
		return Health{}, err
	}

	links, err := linkHealth(c)
	if err != nil {
		return Health{}, err
	}
	return Health{
		Outdated:     outdatedCandidates(c),
		Canonical:    canonicalCandidates(c),
		Supersession: supersessionIssues(c),
		Duplicates:   duplicates(c),
		Links:        links,
	}, nil
}
