package query_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

type fakeLoader struct {
	catalog *domain.Catalog
	err     error
}

func (f fakeLoader) Load() (*domain.Catalog, error) { return f.catalog, f.err }

func article(t *testing.T, id int, category, lifecycle, verdict string) domain.Entry {
	t.Helper()
	habrID := id
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	cat, err := domain.NewCategory(category)
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle(lifecycle)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	v, err := domain.NewVerdict(verdict)
	if err != nil {
		t.Fatalf("verdict: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: "article", Title: "t", Category: cat, Lifecycle: lc,
		HabrID: &habrID, URL: "https://h/x", ReadState: &rs, Verdict: &v,
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	return e
}

func TestStats(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, "golang", "active", "keep"),
		article(t, 2, "golang", "active", "napodumat"),
		article(t, 3, "management", "canonical", "keep"),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	st, err := query.NewService(fakeLoader{catalog: cat}).Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Total != 3 {
		t.Errorf("Total = %d, want 3", st.Total)
	}
	if st.ByCategory["golang"] != 2 || st.ByCategory["management"] != 1 {
		t.Errorf("ByCategory = %v", st.ByCategory)
	}
	if st.ByLifecycle["active"] != 2 || st.ByLifecycle["canonical"] != 1 {
		t.Errorf("ByLifecycle = %v", st.ByLifecycle)
	}
	if st.ByVerdict["keep"] != 2 || st.ByVerdict["napodumat"] != 1 {
		t.Errorf("ByVerdict = %v", st.ByVerdict)
	}
	if st.ByKind["article"] != 3 {
		t.Errorf("ByKind = %v", st.ByKind)
	}
}

func TestEntries(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{article(t, 1, "golang", "active", "keep")})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	entries, err := query.NewService(fakeLoader{catalog: cat}).Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID() != 1 {
		t.Errorf("entries = %v", entries)
	}
}

func TestStats_loaderError(t *testing.T) {
	sentinel := errors.New("boom")
	if _, err := query.NewService(fakeLoader{err: sentinel}).Stats(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}
