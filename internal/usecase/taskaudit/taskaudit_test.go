package taskaudit_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/taskaudit"
)

// entry builds a catalog article entry with the given id, url and optional
// habr_id (0 = none).
func entry(t *testing.T, id int, url string, habrID int) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("golang")
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
	p := domain.EntryParams{
		ID:        id,
		Kind:      domain.KindArticle,
		Title:     "T",
		Category:  cat,
		Lifecycle: lc,
		URL:       url,
		ReadState: &rs,
	}
	if habrID != 0 {
		p.HabrID = &habrID
	}
	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	return e
}

func catalog(t *testing.T, entries ...domain.Entry) *domain.Catalog {
	t.Helper()
	c, err := domain.NewCatalog(entries)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return c
}

func ids(tasks []taskaudit.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.HabrID
	}
	return out
}

func TestAudit(t *testing.T) {
	c := catalog(t,
		entry(t, 1, "https://habr.com/ru/articles/100/", 0),             // present via URL
		entry(t, 2, "https://habr.com/ru/companies/x/articles/200/", 0), // present via URL (company path)
		entry(t, 3, "https://example.com/no-article-id", 300),           // present via habr_id field only
	)

	tasks := []taskaudit.Task{
		{ID: "10", Status: "completed", HabrID: "100"}, // consistent (in catalog)
		{ID: "11", Status: "completed", HabrID: "999"}, // ORPHAN (done, not in catalog)
		{ID: "12", Status: "pending", HabrID: "200"},   // pending but present (can close)
		{ID: "13", Status: "completed", HabrID: ""},    // no habr-id -> ignored
		{ID: "14", Status: "completed", HabrID: "300"}, // consistent via habr_id field
	}

	res := taskaudit.Audit(c, tasks)

	if got := ids(res.Orphans); len(got) != 1 || got[0] != "999" {
		t.Errorf("Orphans = %v, want [999]", got)
	}
	if got := ids(res.PendingPresent); len(got) != 1 || got[0] != "200" {
		t.Errorf("PendingPresent = %v, want [200]", got)
	}
	if len(res.Consistent) != 2 {
		t.Errorf("Consistent = %v, want 2 (100, 300)", ids(res.Consistent))
	}
}

func TestAudit_HasOrphans(t *testing.T) {
	c := catalog(t, entry(t, 1, "https://habr.com/ru/articles/100/", 0))

	clean := taskaudit.Audit(c, []taskaudit.Task{{ID: "1", Status: "completed", HabrID: "100"}})
	if clean.HasOrphans() {
		t.Errorf("HasOrphans() = true, want false for consistent task")
	}

	dirty := taskaudit.Audit(c, []taskaudit.Task{{ID: "2", Status: "completed", HabrID: "404"}})
	if !dirty.HasOrphans() {
		t.Errorf("HasOrphans() = false, want true for orphan task")
	}
}

func TestAudit_StatusCaseInsensitive(t *testing.T) {
	c := catalog(t, entry(t, 1, "https://habr.com/ru/articles/100/", 0))
	res := taskaudit.Audit(c, []taskaudit.Task{{ID: "1", Status: "COMPLETED", HabrID: "100"}})
	if len(res.Consistent) != 1 {
		t.Errorf("Consistent = %d, want 1 (status match must be case-insensitive)", len(res.Consistent))
	}
}
