package audit_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

func daysAgo(now time.Time, days int) *time.Time {
	d := now.AddDate(0, 0, -days)
	return &d
}

func TestCanonicalHealthIssues(t *testing.T) {
	healthy := articleParams{
		title: "Healthy canonical", lifecycle: "canonical",
		description: "has a description", notes: "has notes", relatedIDs: []int{2, 3},
	}
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, healthy),
		// canonical but missing description
		article(t, 2, articleParams{title: "No desc", lifecycle: "canonical", notes: "n", relatedIDs: []int{1}}),
		// canonical but missing notes and related
		article(t, 3, articleParams{title: "No notes/related", lifecycle: "canonical", description: "d"}),
		// non-canonical with nothing → not a canonical-health concern
		article(t, 4, articleParams{title: "Plain", lifecycle: "active"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	svc := audit.NewService(fakeLoader{catalog: cat})
	findings, err := svc.CanonicalHealthIssues()
	if err != nil {
		t.Fatalf("CanonicalHealthIssues: %v", err)
	}

	byID := map[int][]string{}
	for _, f := range findings {
		byID[f.EntryID] = f.Reasons
	}
	if _, ok := byID[1]; ok {
		t.Errorf("healthy canonical 1 should have no issues")
	}
	if _, ok := byID[4]; ok {
		t.Errorf("non-canonical 4 should not be checked")
	}
	if len(byID[2]) != 1 {
		t.Errorf("entry 2 should report exactly 1 issue (missing description), got %v", byID[2])
	}
	if len(byID[3]) != 2 {
		t.Errorf("entry 3 should report 2 issues (missing notes + related), got %v", byID[3])
	}
}

func TestAgeCandidates(t *testing.T) {
	now := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)

	cat, err := domain.NewCatalog([]domain.Entry{
		// ~20 months old habr, active -> candidate
		article(t, 1, articleParams{title: "Old habr", lifecycle: "active", verdict: "keep", dateCreated: daysAgo(now, 610)}),
		// ~6 months old -> not yet
		article(t, 2, articleParams{title: "Recent habr", lifecycle: "active", verdict: "keep", dateCreated: daysAgo(now, 180)}),
		// old but non-habr URL -> not a habr candidate
		article(t, 3, articleParams{title: "Old non-habr", lifecycle: "active", verdict: "keep", url: "https://example.com/x", noHabrID: true, dateCreated: daysAgo(now, 800)}),
		// old habr but already outdated -> skip
		article(t, 4, articleParams{title: "Old already-marked", lifecycle: "outdated", verdict: "keep", dateCreated: daysAgo(now, 800)}),
		// old habr but no date -> can't assess
		article(t, 5, articleParams{title: "No date", lifecycle: "active", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	svc := audit.NewService(fakeLoader{catalog: cat})
	findings, err := svc.AgeCandidates(now)
	if err != nil {
		t.Fatalf("AgeCandidates: %v", err)
	}

	got := map[int]bool{}
	for _, f := range findings {
		got[f.EntryID] = true
	}
	if !got[1] {
		t.Errorf("entry 1 (20mo old habr) should be an age candidate; got %v", got)
	}
	for _, notWanted := range []int{2, 3, 4, 5} {
		if got[notWanted] {
			t.Errorf("entry %d should NOT be an age candidate; got %v", notWanted, got)
		}
	}
}

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
	url := p.url
	if url == "" {
		url = "https://habr.com/x/"
	}
	ep := domain.EntryParams{
		ID:           id,
		Kind:         "article",
		Title:        p.title,
		Category:     cat,
		Lifecycle:    lc,
		URL:          url,
		ReadState:    &rs,
		Description:  p.description,
		RelatedIDs:   p.relatedIDs,
		SupersedesID: p.supersedesID,
		DateCreated:  p.dateCreated,
		Notes:        p.notes,
	}
	if !p.noHabrID {
		ep.HabrID = &habrID
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
	url          string
	dateCreated  *time.Time
	noHabrID     bool
	notes        string
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
		// Points at an entry that is itself marked superseded — a complete merge,
		// which is what "valid" has to mean now that both halves are checked.
		article(t, 4, articleParams{title: "valid super", lifecycle: "active", verdict: "keep", supersedesID: id(6)}),
		article(t, 5, articleParams{title: "no super", lifecycle: "active", verdict: "keep"}),
		article(t, 6, articleParams{title: "replaced by four", lifecycle: "superseded", verdict: "keep"}),
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

func TestDuplicates(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		// same URL → exact-url duplicate
		article(t, 1, articleParams{title: "Alpha one two three", lifecycle: "active", verdict: "keep", url: "https://dup.example/x"}),
		article(t, 2, articleParams{title: "Beta four five six", lifecycle: "active", verdict: "keep", url: "https://dup.example/x"}),
		// same normalized title (часть N stripped) → similar-title duplicate
		article(t, 3, articleParams{title: "Машинное обучение для всех часть 1", lifecycle: "active", verdict: "keep", url: "https://a.example/1"}),
		article(t, 4, articleParams{title: "Машинное обучение для всех часть 2", lifecycle: "active", verdict: "keep", url: "https://a.example/2"}),
		// unique
		article(t, 5, articleParams{title: "Совершенно уникальный заголовок здесь", lifecycle: "active", verdict: "keep", url: "https://u.example/9"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	groups, err := audit.NewService(fakeLoader{catalog: cat}).Duplicates()
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}

	var url, title *audit.DuplicateGroup
	for i := range groups {
		switch groups[i].Kind {
		case "exact-url":
			url = &groups[i]
		case "similar-title":
			title = &groups[i]
		}
	}
	if url == nil || len(url.EntryIDs) != 2 {
		t.Errorf("expected exact-url group of 2, got %+v", url)
	}
	if title == nil || len(title.EntryIDs) != 2 {
		t.Errorf("expected similar-title group of 2 (часть 1/2), got %+v", title)
	}
}

// A duplicate that has already been dealt with is marked superseded, not
// deleted — the catalog keeps the trace of an entry filed twice by two
// different routes. Reporting it forever would mean "dedup is clean" can never
// be a state you reach, only noise you learn to skip.
func TestDuplicates_skipsSuperseded(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		// The pair was merged: 1 survives, 2 was superseded by it.
		article(t, 1, articleParams{title: "Alpha one two three", lifecycle: "active", verdict: "keep", url: "https://dup.example/x"}),
		article(t, 2, articleParams{title: "Beta four five six", lifecycle: "superseded", verdict: "keep", url: "https://dup.example/x"}),
		// A live pair still has to be reported.
		article(t, 3, articleParams{title: "Gamma seven eight nine", lifecycle: "active", verdict: "keep", url: "https://live.example/y"}),
		article(t, 4, articleParams{title: "Delta ten eleven twelve", lifecycle: "active", verdict: "keep", url: "https://live.example/y"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	groups, err := audit.NewService(fakeLoader{catalog: cat}).Duplicates()
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}

	for _, g := range groups {
		for _, id := range g.EntryIDs {
			if id == 2 {
				t.Errorf("superseded entry 2 reported in %s group %q", g.Kind, g.Key)
			}
		}
	}
	if len(groups) != 1 || len(groups[0].EntryIDs) != 2 {
		t.Fatalf("groups = %+v, want exactly the live pair 3/4", groups)
	}
}

func TestOutdatedCandidates_loaderError(t *testing.T) {
	sentinel := errors.New("boom")
	svc := audit.NewService(fakeLoader{err: sentinel})
	if _, err := svc.OutdatedCandidates(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

// A merge has two sides: the surviving entry points at the one it replaced, and
// that one is marked superseded. Writing only one side leaves a pair that looks
// resolved from one direction and untouched from the other.
//
// This became load-bearing when dedup started skipping superseded entries: a
// half-done merge no longer shows up there either, so nothing is left to notice
// it. The catalog is edited by hand, which is exactly when a detector earns its
// keep.
func TestSupersessionIssues_reportsHalfDoneMerges(t *testing.T) {
	target := func(id int) *int { return &id }
	cat, err := domain.NewCatalog([]domain.Entry{
		// Done properly: 1 replaced 2, and 2 says so.
		article(t, 1, articleParams{title: "Survivor entry one", lifecycle: "active", verdict: "keep", supersedesID: target(2)}),
		article(t, 2, articleParams{title: "Replaced entry two", lifecycle: "superseded", verdict: "keep"}),
		// Only the pointer was written: 4 is still active.
		article(t, 3, articleParams{title: "Survivor entry three", lifecycle: "active", verdict: "keep", supersedesID: target(4)}),
		article(t, 4, articleParams{title: "Still active entry four", lifecycle: "active", verdict: "keep"}),
		// Only the mark was written: nobody claims to have replaced 5.
		article(t, 5, articleParams{title: "Orphaned superseded five", lifecycle: "superseded", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	findings, err := audit.NewService(fakeLoader{catalog: cat}).SupersessionIssues()
	if err != nil {
		t.Fatalf("SupersessionIssues: %v", err)
	}
	got := map[int]string{}
	for _, f := range findings {
		got[f.EntryID] = strings.Join(f.Reasons, "; ")
	}

	for _, clean := range []int{1, 2} {
		if _, flagged := got[clean]; flagged {
			t.Errorf("entry %d is a complete merge and must not be flagged: %s", clean, got[clean])
		}
	}
	if _, flagged := got[3]; !flagged {
		t.Error("entry 3 points at an entry that was never marked superseded — not flagged")
	}
	if _, flagged := got[5]; !flagged {
		t.Error("entry 5 is marked superseded with nothing pointing at it — not flagged")
	}
}
