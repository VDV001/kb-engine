package ingest_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

var now = time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)

// params builds valid article EntryParams (id-less) with the given title/url.
func params(t *testing.T, title, url string) domain.EntryParams {
	t.Helper()
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("unread")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	return domain.EntryParams{
		Kind:      domain.KindArticle,
		Title:     title,
		Category:  cat,
		Lifecycle: lc,
		URL:       url,
		ReadState: &rs,
	}
}

// catalogWith builds a catalog from id/url pairs.
func catalogWith(t *testing.T, pairs map[int]string) *domain.Catalog {
	t.Helper()
	var entries []domain.Entry
	for id, url := range pairs {
		p := params(t, "Existing", url)
		p.ID = id
		e, err := domain.NewEntry(p)
		if err != nil {
			t.Fatalf("build entry: %v", err)
		}
		entries = append(entries, e)
	}
	c, err := domain.NewCatalog(entries)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return c
}

func TestPlan_AssignsSequentialIDsAndDateAdded(t *testing.T) {
	c := catalogWith(t, map[int]string{5: "https://h/a", 9: "https://h/b"})

	added, rep, err := ingest.Plan(c, []domain.EntryParams{
		params(t, "New one", "https://h/c"),
		params(t, "New two", "https://h/d"),
	}, now)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if rep.Added != 2 {
		t.Fatalf("Added = %d, want 2", rep.Added)
	}
	if added[0].ID() != 10 || added[1].ID() != 11 {
		t.Errorf("ids = %d,%d, want 10,11", added[0].ID(), added[1].ID())
	}
	if added[0].DateAdded() == nil || added[0].DateAdded().Format("2006-01-02") != "2026-06-24" {
		t.Errorf("DateAdded = %v, want 2026-06-24", added[0].DateAdded())
	}
}

func TestPlan_SkipsExistingURL(t *testing.T) {
	c := catalogWith(t, map[int]string{1: "https://h/dup"})

	added, rep, err := ingest.Plan(c, []domain.EntryParams{
		params(t, "Dup", "https://h/dup"),
		params(t, "Fresh", "https://h/new"),
	}, now)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if rep.Added != 1 || rep.SkippedDuplicate != 1 {
		t.Fatalf("rep = %+v, want Added=1 SkippedDuplicate=1", rep)
	}
	if len(added) != 1 || added[0].URL() != "https://h/new" {
		t.Errorf("added = %v, want only the fresh url", added)
	}
}

func TestPlan_SkipsEmptyURL(t *testing.T) {
	c := catalogWith(t, nil)

	_, rep, err := ingest.Plan(c, []domain.EntryParams{
		params(t, "No url", ""),
		params(t, "Ok", "https://h/ok"),
	}, now)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if rep.Added != 1 || rep.SkippedNoURL != 1 {
		t.Fatalf("rep = %+v, want Added=1 SkippedNoURL=1", rep)
	}
}

func TestPlan_SkipsDuplicateWithinBatch(t *testing.T) {
	c := catalogWith(t, nil)

	added, rep, err := ingest.Plan(c, []domain.EntryParams{
		params(t, "First", "https://h/same"),
		params(t, "Second", "https://h/same"),
	}, now)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if rep.Added != 1 || rep.SkippedDuplicate != 1 {
		t.Fatalf("rep = %+v, want Added=1 SkippedDuplicate=1", rep)
	}
	if len(added) != 1 || added[0].Title() != "First" {
		t.Errorf("kept = %v, want First (first occurrence)", added)
	}
}

func TestPlan_InvalidParamsError(t *testing.T) {
	c := catalogWith(t, nil)
	bad := params(t, "", "https://h/x") // empty title violates the entry invariant

	_, _, err := ingest.Plan(c, []domain.EntryParams{bad}, now)
	if err == nil {
		t.Fatal("expected error for invalid params")
	}
	if !errors.Is(err, domain.ErrInvalidEntry) {
		t.Errorf("err = %v, want ErrInvalidEntry", err)
	}
}
