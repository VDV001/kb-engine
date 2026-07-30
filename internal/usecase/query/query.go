// Package query provides read-only views over the catalog for the dashboard:
// aggregate statistics and an entries snapshot.
package query

import "github.com/daniil/kb-engine/internal/domain"

// CatalogLoader is the port used to obtain the catalog (Dependency Inversion).
type CatalogLoader interface {
	Load() (*domain.Catalog, error)
}

// Stats are aggregate counts over the catalog, plus how the catalog names its
// own categories: a count is of little use to a reader who sees only the key.
type Stats struct {
	Total          int               `json:"total"`
	ByCategory     map[string]int    `json:"by_category"`
	ByLifecycle    map[string]int    `json:"by_lifecycle"`
	ByVerdict      map[string]int    `json:"by_verdict"`
	ByKind         map[string]int    `json:"by_kind"`
	CategoryLabels map[string]string `json:"category_labels,omitempty"`
}

// Service answers read queries over a loaded catalog.
type Service struct {
	loader CatalogLoader
}

// NewService returns a Service backed by loader.
func NewService(loader CatalogLoader) *Service {
	return &Service{loader: loader}
}

// Entries returns all catalog entries.
func (s *Service) Entries() ([]domain.Entry, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return c.Entries(), nil
}

// Stats computes aggregate counts over the catalog.
func (s *Service) Stats() (Stats, error) {
	c, err := s.loader.Load()
	if err != nil {
		return Stats{}, err
	}
	st := Stats{
		ByCategory:     make(map[string]int),
		ByLifecycle:    make(map[string]int),
		ByVerdict:      make(map[string]int),
		ByKind:         make(map[string]int),
		CategoryLabels: c.CategoryLabels(),
	}
	for _, e := range c.Entries() {
		st.Total++
		st.ByCategory[e.Category().String()]++
		st.ByLifecycle[e.Lifecycle().String()]++
		st.ByKind[e.Kind()]++
		if v := e.Verdict(); v != nil {
			st.ByVerdict[v.String()]++
		}
	}
	return st, nil
}

// Health is how far the catalog is from being worked through. Two shares with
// DIFFERENT denominators, because they answer different questions — see Health.
type Health struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	WithNotes int `json:"with_notes"`
	/** Знаменатель второй доли: разобранные статьи, без творений владельца. */
	NotesBase int `json:"notes_base"`
}

// Health computes the two shares the dashboard shows on its health card. They
// deliberately do NOT share a denominator, and there is deliberately no single
// averaged number.
//
// Processed / Total — triage applies to every entry. An entry counts as
// processed when a verdict was recorded, or when it was read. Deliberately not
// the literal statuses the Python dashboard compares against: on the live
// catalog that misses 275 keep and 66 skip entries — all decided — and reports
// 61% where the honest figure is 88%.
//
// WithNotes / NotesBase — depth, over processed ARTICLES only. A write-up for
// an unread article cannot exist, so the 150 unread entries are structurally
// unreachable and have no business in the denominator. The owner's own
// creations are excluded from both sides: there, NotesFile is the document
// itself rather than a write-up of someone else's, and all eight carry one by
// definition.
//
// The two are not averaged into a «health score». 88% and 3% are not
// commensurable, and averaging them announced «45%» about a catalog that is
// almost fully triaged.
func (s *Service) Health() (Health, error) {
	c, err := s.loader.Load()
	if err != nil {
		return Health{}, err
	}
	h := Health{Total: c.Len()}
	for _, e := range c.Entries() {
		if e.Kind() == domain.KindCreation {
			continue
		}
		if e.Verdict() == nil && (e.ReadState() == nil || e.ReadState().String() != "read") {
			continue
		}
		h.Processed++
		h.NotesBase++
		if e.NotesFile() != "" {
			h.WithNotes++
		}
	}
	return h, nil
}
