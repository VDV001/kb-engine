package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Обмен валюты в отчете — отдельной строкой (#432): Summary.Exchanges в
// порядке дат; в траты и разбивки он не входит, как любой перевод себе.
func TestSummarize_exchangesListedApart(t *testing.T) {
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatal(err)
	}
	build := func(id string, day int, p domain.TransactionParams) finance.Record {
		t.Helper()
		p.ID, p.Kind, p.Now = id, domain.KindExpense, clock
		p.Date = time.Date(2026, 4, day, 0, 0, 0, 0, time.UTC)
		tx, err := domain.NewTransaction(p)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := finance.NewRecord(tx, 1, importedAt)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	exchange := func(id string, day int) finance.Record {
		return build(id, day, domain.TransactionParams{
			Amount: domain.NewMoney(900000), Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк",
			Received: domain.ReceivedParams{Amount: domain.NewMoney(10000), Currency: usd, Account: "Wise"},
		})
	}
	recs := []finance.Record{
		exchange("01C", 9),
		build("01A", 5, domain.TransactionParams{Amount: domain.NewMoney(35000), Category: "Еда"}),
		exchange("01B", 6),
	}
	s := finance.Summarize(recs)
	if len(s.Exchanges) != 2 || s.Exchanges[0].ID() != "01B" || s.Exchanges[1].ID() != "01C" {
		t.Fatalf("Exchanges = %v, want 01B, 01C по датам", exchangeIDs(s.Exchanges))
	}
	if s.Expenses != domain.NewMoney(35000) || s.ExpenseCount != 1 {
		t.Fatalf("траты = %v (%d): обмен попал в траты", s.Expenses, s.ExpenseCount)
	}
}

func exchangeIDs(txs []domain.Transaction) []string {
	out := make([]string, len(txs))
	for i, tx := range txs {
		out[i] = tx.ID()
	}
	return out
}
