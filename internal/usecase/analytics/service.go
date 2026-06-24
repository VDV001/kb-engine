package analytics

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// CatalogLoader is the port the analytics service depends on to obtain the
// catalog. The concrete implementation is wired by the caller (DIP).
type CatalogLoader interface {
	Load() (*domain.Catalog, error)
}

// Service computes analytics over a loaded catalog.
type Service struct {
	loader CatalogLoader
}

// NewService returns a Service backed by loader.
func NewService(loader CatalogLoader) *Service {
	return &Service{loader: loader}
}

// Growth returns entry growth over the last `weeks` weeks as of now.
func (s *Service) Growth(now time.Time, weeks int) ([]WeekCount, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return GrowthByWeek(c, now, weeks), nil
}

// Categories returns per-category entry counts, largest first.
func (s *Service) Categories() ([]CategorySize, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return CategorySizes(c), nil
}
