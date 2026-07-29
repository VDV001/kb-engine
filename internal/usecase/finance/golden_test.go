package finance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// goldenPath is shared with the frontend: money.test.ts reads the same file.
// The dashboard totals its own figures rather than taking a summary from the
// server, so this arithmetic exists twice and the two copies have to agree.
const goldenPath = "../../../testdata/finance-golden.json"

type goldenTx struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Date     string `json:"date"`
	Amount   string `json:"amount"`
	Category string `json:"category"`
	Account  string `json:"account"`
	Source   string `json:"source"`
}

type goldenTotal struct {
	Category string `json:"category"`
	Total    string `json:"total"`
	Count    int    `json:"count"`
}

type goldenFile struct {
	Transactions []goldenTx `json:"transactions"`
	Expected     struct {
		ExpenseCount int           `json:"expenseCount"`
		Expenses     string        `json:"expenses"`
		IncomeCount  int           `json:"incomeCount"`
		Income       string        `json:"income"`
		Net          string        `json:"net"`
		ByCategory   []goldenTotal `json:"byCategory"`
		ByAccount    []goldenTotal `json:"byAccount"`
	} `json:"expected"`
}

func TestSummarize_matchesTheSharedGoldenCase(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	clock := func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) }
	recs := make([]finance.Record, 0, len(g.Transactions))
	for _, in := range g.Transactions {
		amount, err := domain.ParseMoney(in.Amount)
		if err != nil {
			t.Fatalf("amount %q: %v", in.Amount, err)
		}
		date, err := time.Parse(time.DateOnly, in.Date)
		if err != nil {
			t.Fatalf("date %q: %v", in.Date, err)
		}
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: in.ID, Kind: in.Kind, Date: date, Amount: amount,
			Category: in.Category, Account: in.Account, Source: in.Source, Now: clock,
		})
		if err != nil {
			t.Fatalf("transaction %s: %v", in.ID, err)
		}
		rec, err := finance.NewRecord(tx, 1, clock())
		if err != nil {
			t.Fatalf("record %s: %v", in.ID, err)
		}
		recs = append(recs, rec)
	}

	s := finance.Summarize(recs)

	if s.ExpenseCount != g.Expected.ExpenseCount || s.Expenses.String() != g.Expected.Expenses {
		t.Errorf("expenses = %s (%d), want %s (%d)",
			s.Expenses, s.ExpenseCount, g.Expected.Expenses, g.Expected.ExpenseCount)
	}
	if s.IncomeCount != g.Expected.IncomeCount || s.Income.String() != g.Expected.Income {
		t.Errorf("income = %s (%d), want %s (%d)",
			s.Income, s.IncomeCount, g.Expected.Income, g.Expected.IncomeCount)
	}
	if s.Net.String() != g.Expected.Net {
		t.Errorf("net = %s, want %s", s.Net, g.Expected.Net)
	}
	compareBreakdown(t, "byCategory", s.ByCategory, g.Expected.ByCategory)
	compareBreakdown(t, "byAccount", s.ByAccount, g.Expected.ByAccount)
}

func compareBreakdown(t *testing.T, name string, got []finance.CategoryTotal, want []goldenTotal) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s has %d rows, want %d: %+v", name, len(got), len(want), got)
	}
	// Order is part of the contract: both sides sort biggest first with the name
	// breaking a draw, so the same data reads the same in the CLI and on screen.
	for i := range want {
		if got[i].Category != want[i].Category || got[i].Total.String() != want[i].Total || got[i].Count != want[i].Count {
			t.Errorf("%s[%d] = %q %s (%d), want %q %s (%d)", name, i,
				got[i].Category, got[i].Total, got[i].Count,
				want[i].Category, want[i].Total, want[i].Count)
		}
	}
}
