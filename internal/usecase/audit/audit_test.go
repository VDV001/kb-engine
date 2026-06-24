package audit_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// fakeLoader is a test double for the CatalogLoader port.
type fakeLoader struct {
	catalog *domain.Catalog
	err     error
}

func (f fakeLoader) Load() (*domain.Catalog, error) { return f.catalog, f.err }

func article(t *testing.T, id int, p articleParams) domain.Entry {
	t.Helper()
	habrID := id
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle(p.lifecycle)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	ep := domain.EntryParams{
		ID:          id,
		Kind:        "article",
		Title:       p.title,
		Category:    cat,
		Lifecycle:   lc,
		HabrID:      &habrID,
		URL:         "https://habr.com/x/",
		ReadState:   &rs,
		Description: p.description,
	}
	if p.verdict != "" {
		v, err := domain.NewVerdict(p.verdict)
		if err != nil {
			t.Fatalf("verdict: %v", err)
		}
		ep.Verdict = &v
	}
	e, err := domain.NewEntry(ep)
	if err != nil {
		t.Fatalf("entry %d: %v", id, err)
	}
	return e
}

type articleParams struct {
	title       string
	description string
	lifecycle   string
	verdict     string
}

func TestOutdatedCandidates(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, articleParams{title: "Fresh take", description: "all good", lifecycle: "active", verdict: "keep"}),
		article(t, 2, articleParams{title: "Статья удалена автором", description: "x", lifecycle: "active", verdict: "keep"}),
		article(t, 3, articleParams{title: "Deprecated approach", description: "no longer available", lifecycle: "active", verdict: "keep"}),
		article(t, 4, articleParams{title: "Gone", description: "y", lifecycle: "active", verdict: "skip-unavailable"}),
		article(t, 5, articleParams{title: "удалён", description: "z", lifecycle: "outdated", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	svc := audit.NewService(fakeLoader{catalog: cat})
	findings, err := svc.OutdatedCandidates()
	if err != nil {
		t.Fatalf("OutdatedCandidates: %v", err)
	}

	got := map[int]bool{}
	for _, f := range findings {
		got[f.EntryID] = true
	}

	// 1 is clean → no. 2,3 keyword. 4 skip-unavailable. 5 already outdated → skip.
	for _, want := range []int{2, 3, 4} {
		if !got[want] {
			t.Errorf("entry %d expected as candidate, missing", want)
		}
	}
	for _, notWant := range []int{1, 5} {
		if got[notWant] {
			t.Errorf("entry %d should not be a candidate", notWant)
		}
	}
}

func TestOutdatedCandidates_wordBoundary(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 10, articleParams{title: "Удалёнка убивает тело", lifecycle: "active", verdict: "keep"}),
		article(t, 11, articleParams{title: "Снятся ли ИИ овцы", lifecycle: "active", verdict: "keep"}),
		article(t, 12, articleParams{title: "Решение удаления терминов", lifecycle: "active", verdict: "keep"}),
		article(t, 13, articleParams{title: "Статья удалена автором", lifecycle: "active", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	findings, err := audit.NewService(fakeLoader{catalog: cat}).OutdatedCandidates()
	if err != nil {
		t.Fatalf("OutdatedCandidates: %v", err)
	}
	got := map[int]bool{}
	for _, f := range findings {
		got[f.EntryID] = true
	}

	for _, notWant := range []int{10, 11, 12} { // удалёнка / снятся / удаления — different words
		if got[notWant] {
			t.Errorf("entry %d falsely flagged (substring false positive)", notWant)
		}
	}
	if !got[13] { // "удалена" is a real removal signal
		t.Error("entry 13 (удалена) should be flagged")
	}
}

func TestOutdatedCandidates_loaderError(t *testing.T) {
	sentinel := errors.New("boom")
	svc := audit.NewService(fakeLoader{err: sentinel})
	if _, err := svc.OutdatedCandidates(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}
