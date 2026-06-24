package analytics_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
)

type fakeLoader struct {
	c   *domain.Catalog
	err error
}

func (f fakeLoader) Load() (*domain.Catalog, error) { return f.c, f.err }

func TestService(t *testing.T) {
	now := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	c, err := domain.NewCatalog([]domain.Entry{
		mkEntry(t, 1, "golang", at(2026, 6, 23)),
		mkEntry(t, 2, "frontend", at(2026, 6, 23)),
		mkEntry(t, 3, "golang", nil),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	svc := analytics.NewService(fakeLoader{c: c})

	growth, err := svc.Growth(now, 4)
	if err != nil {
		t.Fatalf("Growth: %v", err)
	}
	if len(growth) != 4 || growth[3].Count != 2 {
		t.Errorf("growth = %+v, want 4 weeks with latest count 2", growth)
	}

	cats, err := svc.Categories()
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if len(cats) != 2 || cats[0].Category != "golang" || cats[0].Count != 2 {
		t.Errorf("categories = %+v, want golang/2 on top", cats)
	}
}

func TestService_LoadError(t *testing.T) {
	svc := analytics.NewService(fakeLoader{err: errors.New("boom")})
	if _, err := svc.Growth(time.Now(), 4); err == nil {
		t.Error("Growth should propagate loader error")
	}
	if _, err := svc.Categories(); err == nil {
		t.Error("Categories should propagate loader error")
	}
}
