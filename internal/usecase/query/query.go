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
