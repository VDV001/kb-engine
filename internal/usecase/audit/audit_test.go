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
		ID:           id,
		Kind:         "article",
		Title:        p.title,
		Category:     cat,
		Lifecycle:    lc,
		HabrID:       &habrID,
		URL:          "https://habr.com/x/",
		ReadState:    &rs,
		Description:  p.description,
		RelatedIDs:   p.relatedIDs,
		SupersedesID: p.supersedesID,
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
	title        string
	description  string
	lifecycle    string
	verdict      string
	relatedIDs   []int
	supersedesID *int
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

func TestCanonicalCandidates(t *testing.T) {
	ref := func(id int, target int, lifecycle string) domain.Entry {
		return article(t, id, articleParams{title: "t", lifecycle: lifecycle, verdict: "keep", relatedIDs: []int{target}})
	}
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, articleParams{title: "Highly referenced", lifecycle: "active", verdict: "keep"}),
		ref(10, 1, "active"), ref(11, 1, "active"), ref(12, 1, "active"), // 1 referenced 3x
		article(t, 2, articleParams{title: "Barely referenced", lifecycle: "active", verdict: "keep"}),
		ref(13, 2, "active"), // 2 referenced 1x
		article(t, 3, articleParams{title: "Already canonical", lifecycle: "canonical", verdict: "keep"}),
		ref(14, 3, "active"), ref(15, 3, "active"), ref(16, 3, "active"), // 3 referenced 3x but canonical
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	findings, err := audit.NewService(fakeLoader{catalog: cat}).CanonicalCandidates()
	if err != nil {
		t.Fatalf("CanonicalCandidates: %v", err)
	}
	got := map[int]bool{}
	for _, f := range findings {
		got[f.EntryID] = true
	}
	if !got[1] {
		t.Error("entry 1 (referenced 3x) should be a canonical candidate")
	}
	if got[2] {
		t.Error("entry 2 (referenced 1x) should not be a candidate")
	}
	if got[3] {
		t.Error("entry 3 (already canonical) should not be a candidate")
	}
}

func TestSupersessionIssues(t *testing.T) {
	id := func(n int) *int { return &n }
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, articleParams{title: "dangling", lifecycle: "active", verdict: "keep", supersedesID: id(99)}),
		article(t, 2, articleParams{title: "cycle-a", lifecycle: "active", verdict: "keep", supersedesID: id(3)}),
		article(t, 3, articleParams{title: "cycle-b", lifecycle: "active", verdict: "keep", supersedesID: id(2)}),
		article(t, 4, articleParams{title: "valid super", lifecycle: "active", verdict: "keep", supersedesID: id(1)}),
		article(t, 5, articleParams{title: "no super", lifecycle: "active", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	findings, err := audit.NewService(fakeLoader{catalog: cat}).SupersessionIssues()
	if err != nil {
		t.Fatalf("SupersessionIssues: %v", err)
	}
	got := map[int]bool{}
	for _, f := range findings {
		got[f.EntryID] = true
	}
	if !got[1] {
		t.Error("entry 1 (supersedes a missing id 99) should be flagged")
	}
	if !got[2] || !got[3] {
		t.Error("entries 2 and 3 (cycle) should be flagged")
	}
	if got[4] {
		t.Error("entry 4 (supersedes existing id 1) should not be flagged")
	}
	if got[5] {
		t.Error("entry 5 (no supersedes_id) should not be flagged")
	}
}

func TestOutdatedCandidates_loaderError(t *testing.T) {
	sentinel := errors.New("boom")
	svc := audit.NewService(fakeLoader{err: sentinel})
	if _, err := svc.OutdatedCandidates(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}
