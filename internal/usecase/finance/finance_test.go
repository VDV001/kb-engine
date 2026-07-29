package finance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

var (
	importedAt = time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	clock      = func() time.Time { return importedAt }
)

// tx builds a valid expense with a positional id, the way the xlsx reader
// hands them over.
func tx(t *testing.T, id string, day int, amount int64, category string) domain.Transaction {
	t.Helper()
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:       id,
		Kind:     domain.KindExpense,
		Date:     time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC),
		Amount:   domain.NewMoney(amount),
		Category: category,
		Now:      clock,
	})
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

// ids returns a generator handing out the given ids in order, then failing the
// test if asked for more than were prepared.
func ids(t *testing.T, values ...string) func() string {
	t.Helper()
	i := 0
	return func() string {
		if i >= len(values) {
			t.Fatalf("id generator called %d times, only %d ids prepared", i+1, len(values))
		}
		v := values[i]
		i++
		return v
	}
}

func TestImport_assignsStableIdentityAndFirstRevision(t *testing.T) {
	in := []domain.Transaction{
		tx(t, "expense-r3", 29, 20245, "Еда"),
		tx(t, "expense-r4", 30, 50000, "Транспорт"),
	}

	recs, err := finance.Import(in, ids(t, "01A", "01B"), importedAt)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	for i, want := range []string{"01A", "01B"} {
		if got := recs[i].Transaction().ID(); got != want {
			t.Errorf("record %d id = %q, want %q", i, got, want)
		}
		if got := recs[i].Rev(); got != 1 {
			t.Errorf("record %d rev = %d, want 1 on first import", i, got)
		}
		if !recs[i].UpdatedAt().Equal(importedAt) {
			t.Errorf("record %d updatedAt = %s, want the import clock", i, recs[i].UpdatedAt())
		}
	}
	// Nothing but the identity may change: amounts are the whole point.
	if got := recs[0].Transaction().Amount().Kopecks(); got != 20245 {
		t.Errorf("amount = %d kopecks, want 20245 — import must not touch the money", got)
	}
}

// A generator that repeats itself would silently collapse two rows into one on
// the next load, which fails closed there — but the cheapest place to catch it
// is where the ids are handed out.
func TestImport_rejectsDuplicateID(t *testing.T) {
	in := []domain.Transaction{
		tx(t, "expense-r3", 29, 20245, "Еда"),
		tx(t, "expense-r4", 30, 50000, "Транспорт"),
	}
	_, err := finance.Import(in, ids(t, "01A", "01A"), importedAt)
	if !errors.Is(err, finance.ErrDuplicateID) {
		t.Errorf("Import() error = %v, want ErrDuplicateID", err)
	}
}

func TestAdd_buildsAFirstRevisionRecord(t *testing.T) {
	rec, err := finance.Add(finance.AddParams{
		Kind:        domain.KindExpense,
		Amount:      domain.NewMoney(150000),
		Category:    "Еда",
		Description: "продукты",
	}, ids(t, "01C"), clock)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if rec.Transaction().ID() != "01C" || rec.Rev() != 1 {
		t.Errorf("got id=%q rev=%d, want id=01C rev=1", rec.Transaction().ID(), rec.Rev())
	}
	// An omitted date means "today" — resolved from the injected clock, in the
	// use case. A handler that filled it in would be reaching for the wall clock
	// on the wrong side of the boundary.
	if got := rec.Transaction().Date(); !got.Equal(time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %s, want today at midnight UTC", got)
	}
}

func TestAdd_propagatesDomainInvariants(t *testing.T) {
	_, err := finance.Add(finance.AddParams{
		Kind:     domain.KindExpense,
		Amount:   domain.NewMoney(0),
		Category: "Еда",
	}, ids(t, "01D"), clock)
	if !errors.Is(err, domain.ErrInvalidTransaction) {
		t.Errorf("Add() error = %v, want it to carry ErrInvalidTransaction", err)
	}
}

func TestNewRecord_invariants(t *testing.T) {
	valid := tx(t, "01A", 29, 20245, "Еда")
	tests := []struct {
		name      string
		rev       int
		updatedAt time.Time
	}{
		{"zero revision", 0, importedAt},
		{"negative revision", -1, importedAt},
		{"missing updatedAt", 1, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := finance.NewRecord(valid, tt.rev, tt.updatedAt); !errors.Is(err, finance.ErrInvalidRecord) {
				t.Errorf("NewRecord() error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

// The file is read by humans and diffed by git, so its order has to be stable
// and meaningful: chronological, with the id breaking ties.
func TestSort_chronologicalWithIDAsTiebreak(t *testing.T) {
	recs := []finance.Record{
		mustRecord(t, tx(t, "01C", 30, 100, "Еда")),
		mustRecord(t, tx(t, "01B", 29, 100, "Еда")),
		mustRecord(t, tx(t, "01A", 29, 100, "Еда")),
	}
	finance.Sort(recs)
	var got []string
	for _, r := range recs {
		got = append(got, r.Transaction().ID())
	}
	want := []string{"01A", "01B", "01C"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func mustRecord(t *testing.T, transaction domain.Transaction) finance.Record {
	t.Helper()
	r, err := finance.NewRecord(transaction, 1, importedAt)
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}
	return r
}
