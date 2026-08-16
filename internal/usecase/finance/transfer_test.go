package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Сводка не должна считать перекладывание денег тратой. На живой книге такая
// запись была одна, и из-за неё веб и терминал показывали разные расходы:
// расхождение ровно на её сумму.
func TestSummarize_leavesInternalTransfersOutOfTheTotals(t *testing.T) {
	recs := []finance.Record{
		transferRecord(t, "2026-05-04", "2000"),
		expenseRecordOn(t, "2026-05-05", "500", "Еда", "Продукты"),
	}

	s := finance.Summarize(recs)

	if s.Expenses.Kopecks() != 50000 {
		t.Errorf("расходы = %s, ожидалось 500.00 без перевода", s.Expenses)
	}
	if s.ExpenseCount != 1 {
		t.Errorf("расходов посчитано %d, ожидалась 1", s.ExpenseCount)
	}
	// И в разбивке его тоже нет: строка «Прочее» с переводом внутри сделала бы
	// категорию, которой человек не тратил.
	for _, c := range s.ByCategory {
		if c.Category == "Прочее" {
			t.Errorf("перевод попал в разбивку по категориям: %s %s", c.Category, c.Total)
		}
	}
}

// Итог тоже без переводов. Перевод между своими счетами не меняет, сколько
// денег у человека всего, — значит и на «доходы минус расходы» влиять не может.
func TestSummarize_transferDoesNotMoveTheNet(t *testing.T) {
	withTransfer := finance.Summarize([]finance.Record{
		incomeRecordOn(t, "2026-05-01", "1000", "Зарплата"),
		transferRecord(t, "2026-05-04", "2000"),
	})
	withoutIt := finance.Summarize([]finance.Record{
		incomeRecordOn(t, "2026-05-01", "1000", "Зарплата"),
	})

	if withTransfer.Net != withoutIt.Net {
		t.Errorf("итог с переводом %s, без него %s — перевод не меняет, сколько денег всего",
			withTransfer.Net, withoutIt.Net)
	}
}

// Доход «Перевод себе» тоже не доход.
func TestSummarize_incomeTransferIsNotIncome(t *testing.T) {
	s := finance.Summarize([]finance.Record{
		incomeRecordOn(t, "2026-05-01", "1000", "Зарплата"),
		incomeRecordOn(t, "2026-05-02", "5000", "Перевод себе"),
	})

	if s.Income.Kopecks() != 100000 {
		t.Errorf("доходы = %s, ожидалось 1000.00 без перевода себе", s.Income)
	}
	if s.IncomeCount != 1 {
		t.Errorf("доходов посчитано %d, ожидался 1", s.IncomeCount)
	}
}

func transferRecord(t *testing.T, date, amount string) finance.Record {
	t.Helper()
	return expenseRecordOn(t, date, amount, "Прочее", "Перевод себе")
}

func expenseRecordOn(t *testing.T, date, amount, cat, sub string) finance.Record {
	t.Helper()
	return recordFrom(t, finance.AddParams{
		Kind: domain.KindExpense, Date: mustDay(t, date), Amount: mustMoney(t, amount),
		Category: cat, Subcategory: sub,
	})
}

func incomeRecordOn(t *testing.T, date, amount, source string) finance.Record {
	t.Helper()
	return recordFrom(t, finance.AddParams{
		Kind: domain.KindIncome, Date: mustDay(t, date), Amount: mustMoney(t, amount), Source: source,
	})
}

func recordFrom(t *testing.T, p finance.AddParams) finance.Record {
	t.Helper()
	rec, err := finance.Add(p, func() string { return "01T" + p.Date.Format("0102") }, time.Now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}

func mustMoney(t *testing.T, raw string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(raw)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", raw, err)
	}
	return m
}

func mustDay(t *testing.T, raw string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return d
}
