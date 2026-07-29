package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// ledger builds a small mixed ledger: two March expenses in different
// categories, one April expense, one April income.
func ledger(t *testing.T) []finance.Record {
	t.Helper()
	build := func(id, kind string, y int, m time.Month, d int, amount int64, category string) finance.Record {
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID:       id,
			Kind:     kind,
			Date:     time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
			Amount:   domain.NewMoney(amount),
			Category: category,
			Now:      clock,
		})
		if err != nil {
			t.Fatalf("build transaction: %v", err)
		}
		rec, err := finance.NewRecord(tx, 1, importedAt)
		if err != nil {
			t.Fatalf("build record: %v", err)
		}
		return rec
	}
	return []finance.Record{
		build("01A", domain.KindExpense, 2026, time.March, 29, 20245, "Еда"),
		build("01B", domain.KindExpense, 2026, time.March, 30, 150000, "Транспорт"),
		build("01C", domain.KindExpense, 2026, time.April, 2, 30000, "Еда"),
		build("01D", domain.KindIncome, 2026, time.April, 5, 9000000, ""),
	}
}

func TestMatch(t *testing.T) {
	recs := ledger(t)
	tests := []struct {
		name   string
		filter finance.Filter
		want   []string
	}{
		{"everything by default", finance.Filter{}, []string{"01A", "01B", "01C", "01D"}},
		{"one month", finance.Filter{Year: 2026, Month: time.March}, []string{"01A", "01B"}},
		{"month without a year spans years", finance.Filter{Month: time.April}, []string{"01C", "01D"}},
		{"category", finance.Filter{Category: "Еда"}, []string{"01A", "01C"}},
		// The ledger is typed by hand; "еда" and "Еда" are the same category to
		// everyone except a case-sensitive comparison.
		{"category ignores case", finance.Filter{Category: "еДа"}, []string{"01A", "01C"}},
		{"kind", finance.Filter{Kind: domain.KindIncome}, []string{"01D"}},
		{"combined", finance.Filter{Year: 2026, Month: time.April, Kind: domain.KindExpense}, []string{"01C"}},
		{"no match is empty, not everything", finance.Filter{Category: "Ничего"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finance.Match(recs, tt.filter)
			if len(got) != len(tt.want) {
				t.Fatalf("matched %d records, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if id := got[i].Transaction().ID(); id != tt.want[i] {
					t.Errorf("record %d = %q, want %q", i, id, tt.want[i])
				}
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	s := finance.Summarize(ledger(t))

	if s.ExpenseCount != 3 || s.IncomeCount != 1 {
		t.Errorf("counts = %d expenses / %d income, want 3 / 1", s.ExpenseCount, s.IncomeCount)
	}
	if got := s.Expenses.Kopecks(); got != 200245 {
		t.Errorf("expenses = %d kopecks, want 200245", got)
	}
	if got := s.Income.Kopecks(); got != 9000000 {
		t.Errorf("income = %d kopecks, want 9000000", got)
	}
	// Net is what the balance actually moved by, so it has to be income minus
	// expenses and not a second, independently computed number.
	if got := s.Net.Kopecks(); got != 9000000-200245 {
		t.Errorf("net = %d kopecks, want %d", got, 9000000-200245)
	}

	// Biggest first: the point of the breakdown is what to look at.
	want := []struct {
		category string
		kopecks  int64
		count    int
	}{
		{"Транспорт", 150000, 1},
		{"Еда", 50245, 2},
	}
	if len(s.ByCategory) != len(want) {
		t.Fatalf("got %d categories, want %d", len(s.ByCategory), len(want))
	}
	for i, w := range want {
		got := s.ByCategory[i]
		if got.Category != w.category || got.Total.Kopecks() != w.kopecks || got.Count != w.count {
			t.Errorf("category %d = %s %d kopecks x%d, want %s %d x%d",
				i, got.Category, got.Total.Kopecks(), got.Count, w.category, w.kopecks, w.count)
		}
	}
}

// A refund is a negative expense, so it must reduce its category rather than
// inflate it — the ledger has two of them in April.
func TestSummarize_refundReducesItsCategory(t *testing.T) {
	recs := ledger(t)
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID:          "01E",
		Kind:        domain.KindExpense,
		Date:        time.Date(2026, time.April, 6, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(-10000),
		Category:    "Еда",
		Description: "возврат",
		Now:         clock,
	})
	if err != nil {
		t.Fatalf("build refund: %v", err)
	}
	rec, err := finance.NewRecord(tx, 1, importedAt)
	if err != nil {
		t.Fatalf("build record: %v", err)
	}

	s := finance.Summarize(append(recs, rec))
	if got := s.Expenses.Kopecks(); got != 190245 {
		t.Errorf("expenses = %d kopecks, want 190245 (refund taken off)", got)
	}
	for _, c := range s.ByCategory {
		if c.Category == "Еда" && c.Total.Kopecks() != 40245 {
			t.Errorf("Еда total = %d kopecks, want 40245", c.Total.Kopecks())
		}
	}
}

func TestSummarize_emptyLedger(t *testing.T) {
	s := finance.Summarize(nil)
	if s.ExpenseCount != 0 || s.IncomeCount != 0 || !s.Net.IsZero() || len(s.ByCategory) != 0 {
		t.Errorf("empty ledger summary = %+v, want zeros", s)
	}
}
