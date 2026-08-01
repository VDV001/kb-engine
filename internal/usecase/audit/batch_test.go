package audit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// batchEntry builds an entry belonging to an import batch, with the three
// fields the batch determines.
func batchEntry(t *testing.T, id, batch int, source, sourceDate, dateAdded string) domain.Entry {
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
		ID: id, Kind: domain.KindArticle, Title: "Entry", Category: cat,
		Lifecycle: lc, ReadState: &rs, Source: source,
	}
	if batch != 0 {
		b := batch
		p.SourceBatch = &b
	}
	p.SourceDate = parseDay(t, sourceDate)
	p.DateAdded = parseDay(t, dateAdded)

	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

func parseDay(t *testing.T, s string) *time.Time {
	t.Helper()
	if s == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return &d
}

// The batch is stored on every entry rather than in a table of its own: a
// deliberate denormalization, recorded in ADR-0002. What makes it safe is this
// check — on the live catalog all 327 entries across 4 batches agree, and the
// engine now says so instead of the owner having to trust it.
func TestBatchConsistencyIssues(t *testing.T) {
	t.Run("a consistent batch is silent", func(t *testing.T) {
		c := catalogOf(t,
			batchEntry(t, 1, 20, "batch", "2026-05-25", "2026-05-25"),
			batchEntry(t, 2, 20, "batch", "2026-05-25", "2026-05-25"),
		)
		svc := audit.NewService(fixedLoader{c})

		findings, err := svc.BatchConsistencyIssues()
		if err != nil {
			t.Fatalf("BatchConsistencyIssues: %v", err)
		}
		if len(findings) != 0 {
			t.Fatalf("got %d finding(s), want none: %+v", len(findings), findings)
		}
	})

	t.Run("a disagreeing field is reported once per entry that differs", func(t *testing.T) {
		c := catalogOf(t,
			batchEntry(t, 1, 20, "batch", "2026-05-25", "2026-05-25"),
			batchEntry(t, 2, 20, "batch", "2026-05-25", "2026-05-25"),
			batchEntry(t, 3, 20, "bot-inbox", "2026-05-25", "2026-05-25"),
		)
		svc := audit.NewService(fixedLoader{c})

		findings, err := svc.BatchConsistencyIssues()
		if err != nil {
			t.Fatalf("BatchConsistencyIssues: %v", err)
		}
		if len(findings) != 1 {
			t.Fatalf("got %d finding(s), want 1: %+v", len(findings), findings)
		}
		if findings[0].EntryID != 3 {
			t.Errorf("EntryID = %d, want 3 (the entry that differs, not the majority)", findings[0].EntryID)
		}
		joined := strings.Join(findings[0].Reasons, " ")
		for _, want := range []string{"20", "source", "bot-inbox", "batch"} {
			if !strings.Contains(joined, want) {
				t.Errorf("reasons %q do not mention %q", joined, want)
			}
		}
	})

	t.Run("entries outside any batch are not compared", func(t *testing.T) {
		c := catalogOf(t,
			batchEntry(t, 1, 0, "ad-hoc", "2026-05-01", "2026-05-01"),
			batchEntry(t, 2, 0, "digest", "2026-06-01", "2026-06-01"),
		)
		svc := audit.NewService(fixedLoader{c})

		findings, err := svc.BatchConsistencyIssues()
		if err != nil {
			t.Fatalf("BatchConsistencyIssues: %v", err)
		}
		if len(findings) != 0 {
			t.Fatalf("got %d finding(s), want none: %+v", len(findings), findings)
		}
	})

	// A batch where a field is absent everywhere is consistent. Batch 11 on the
	// live catalog carries neither source_date nor date_added, and reporting
	// that as drift would bury a real disagreement.
	t.Run("a field absent across the whole batch is consistent", func(t *testing.T) {
		c := catalogOf(t,
			batchEntry(t, 1, 11, "bot-inbox", "", ""),
			batchEntry(t, 2, 11, "bot-inbox", "", ""),
		)
		svc := audit.NewService(fixedLoader{c})

		findings, err := svc.BatchConsistencyIssues()
		if err != nil {
			t.Fatalf("BatchConsistencyIssues: %v", err)
		}
		if len(findings) != 0 {
			t.Fatalf("got %d finding(s), want none: %+v", len(findings), findings)
		}
	})

	// An entry missing a date the rest of its batch has is drift too: the batch
	// determines the field, so a hole in it is a disagreement.
	t.Run("a hole where the batch has a value is drift", func(t *testing.T) {
		c := catalogOf(t,
			batchEntry(t, 1, 13, "bot-inbox", "", "2026-05-08"),
			batchEntry(t, 2, 13, "bot-inbox", "", "2026-05-08"),
			batchEntry(t, 3, 13, "bot-inbox", "", ""),
		)
		svc := audit.NewService(fixedLoader{c})

		findings, err := svc.BatchConsistencyIssues()
		if err != nil {
			t.Fatalf("BatchConsistencyIssues: %v", err)
		}
		if len(findings) != 1 || findings[0].EntryID != 3 {
			t.Fatalf("got %+v, want one finding on entry 3", findings)
		}
	})
}
