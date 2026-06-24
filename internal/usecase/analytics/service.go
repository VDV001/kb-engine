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

// Service computes analytics over a loaded catalog. It owns its clock so the
// delivery layer (HTTP handler) never reads the wall clock itself.
type Service struct {
	loader CatalogLoader
	now    func() time.Time
}

// NewService returns a Service backed by loader, using the real wall clock.
func NewService(loader CatalogLoader) *Service {
	return &Service{loader: loader, now: time.Now}
}

// NewServiceWithClock is NewService with an injectable clock, for deterministic
// tests.
func NewServiceWithClock(loader CatalogLoader, now func() time.Time) *Service {
	return &Service{loader: loader, now: now}
}

// Growth returns entry growth over the last `weeks` weeks, as of the service's
// clock.
func (s *Service) Growth(weeks int) ([]WeekCount, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return GrowthByWeek(c, s.now(), weeks), nil
}

// Categories returns per-category entry counts, largest first.
func (s *Service) Categories() ([]CategorySize, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return CategorySizes(c), nil
}
