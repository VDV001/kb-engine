package audit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

func linkEntry(t *testing.T, id int, url, checked string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("ai-agents-tools")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	p := domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: "T", Category: cat,
		Lifecycle: lc, ReadState: &rs, URL: url,
	}
	p.DriftCheckDate = parseDay(t, checked)
	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

// The base must be able to say which of its own links it has never asked about.
// On the live catalog that was 527 entries, and nothing on any screen said so —
// which is exactly how a knowledge base fills with links nobody knows are real.
func TestUncheckedLinkIssues(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := catalogOf(t,
		linkEntry(t, 1, "https://example.com/never", ""),
		linkEntry(t, 2, "https://example.com/may", "2026-05-16"),
		linkEntry(t, 3, "https://example.com/fresh", "2026-07-28"),
		linkEntry(t, 4, "", ""), // no url — nothing to check
	)

	findings, err := audit.NewService(fixedLoader{c}).UncheckedLinkIssues(now)
	if err != nil {
		t.Fatalf("UncheckedLinkIssues: %v", err)
	}

	ids := map[int]string{}
	for _, f := range findings {
		ids[f.EntryID] = strings.Join(f.Reasons, " ")
	}
	if _, ok := ids[1]; !ok {
		t.Error("an entry never checked was not reported")
	}
	if _, ok := ids[2]; !ok {
		t.Error("an entry last checked in May was not reported as stale")
	}
	if _, ok := ids[3]; ok {
		t.Error("a recently checked entry was reported")
	}
	if _, ok := ids[4]; ok {
		t.Error("an entry without a url was reported — there is nothing to check")
	}
	if !strings.Contains(ids[1], "ни разу") {
		t.Errorf("reason %q does not distinguish «never checked» from «checked long ago»", ids[1])
	}
	if !strings.Contains(ids[2], "2026-05-16") {
		t.Errorf("reason %q does not say when it was last checked", ids[2])
	}
}
