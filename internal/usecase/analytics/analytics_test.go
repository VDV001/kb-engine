package analytics_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
)

func mkEntry(t *testing.T, id int, category string, created *time.Time) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory(category)
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: "T", Category: cat, Lifecycle: lc,
		ReadState: &rs, DateCreated: created,
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	return e
}

func at(y int, m time.Month, d int) *time.Time {
	tt := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &tt
}

func TestGrowthByWeek(t *testing.T) {
	now := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	cat, err := domain.NewCatalog([]domain.Entry{
		mkEntry(t, 1, "golang", at(2026, 6, 23)), // 1 day ago  -> week 0
		mkEntry(t, 2, "golang", at(2026, 6, 20)), // 4 days ago -> week 0
		mkEntry(t, 3, "golang", at(2026, 6, 15)), // 9 days ago -> week 1
		mkEntry(t, 4, "golang", at(2025, 1, 1)),  // way older  -> ignored
		mkEntry(t, 5, "golang", nil),             // no date    -> ignored
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	got := analytics.GrowthByWeek(cat, now, 4)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 weeks", len(got))
	}
	// Oldest first, newest last.
	if got[3].Count != 2 {
		t.Errorf("latest week count = %d, want 2 (week 0)", got[3].Count)
	}
	if got[2].Count != 1 {
		t.Errorf("week 1 count = %d, want 1", got[2].Count)
	}
	if got[0].Count != 0 || got[1].Count != 0 {
		t.Errorf("older weeks should be empty: %+v", got)
	}
	// Week label is DD.MM of that week's start (7 days per bucket back from now).
	if got[3].Week != "17.06" {
		t.Errorf("latest week label = %q, want 17.06", got[3].Week)
	}
}

func TestCategorySizes(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		mkEntry(t, 1, "golang", nil),
		mkEntry(t, 2, "golang", nil),
		mkEntry(t, 3, "golang", nil),
		mkEntry(t, 4, "frontend", nil),
		mkEntry(t, 5, "devops", nil),
		mkEntry(t, 6, "devops", nil),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	got := analytics.CategorySizes(cat)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Sorted by count desc.
	if got[0].Category != "golang" || got[0].Count != 3 {
		t.Errorf("top = %+v, want golang/3", got[0])
	}
	if got[1].Category != "devops" || got[1].Count != 2 {
		t.Errorf("second = %+v, want devops/2", got[1])
	}
	if got[2].Category != "frontend" || got[2].Count != 1 {
		t.Errorf("third = %+v, want frontend/1", got[2])
	}
}
