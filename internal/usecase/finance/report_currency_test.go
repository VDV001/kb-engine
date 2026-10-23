package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Доллары и сумы не складываются с рублями: без курса сумма «100 ₽ + $12,30»
// не значит ничего, а с курсом это уже оценка, а не итог. Записи в другой
// валюте проходят мимо рублёвых сумм и разбивок, как внутренние переводы, и
// считаются отдельно по каждой валюте — иначе отчёт молча завысил бы траты.
func TestSummarize_keepsOtherCurrenciesApart(t *testing.T) {
	add := func(kind, category, source string, kopecks int64, code string) finance.Record {
		t.Helper()
		p := finance.AddParams{
			Kind: kind, Date: day(2026, time.October, 22), Amount: domain.NewMoney(kopecks),
			Category: category, Source: source,
		}
		if code != "" {
			p.Currency = cur(t, code)
		}
		rec, err := finance.Add(p, func() string { return "01" + code + category + source }, currencyNow)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		return rec
	}
	recs := []finance.Record{
		add(domain.KindExpense, "Еда", "", 10000, ""),
		add(domain.KindExpense, "Сервисы", "", 1230, "USD"),
		add(domain.KindExpense, "Еда", "", 500, "USD"),
		add(domain.KindIncome, "", "Заказчик", 50000000, "UZS"),
	}

	s := finance.Summarize(recs)

	if s.Expenses != domain.NewMoney(10000) || s.ExpenseCount != 1 {
		t.Errorf("рублёвые траты = %s (%d), want 100.00 (1)", s.Expenses, s.ExpenseCount)
	}
	if !s.Income.IsZero() || s.IncomeCount != 0 {
		t.Errorf("рублёвые доходы = %s (%d), want 0", s.Income, s.IncomeCount)
	}
	if s.Net != domain.NewMoney(-10000) {
		t.Errorf("Net = %s, want -100.00", s.Net)
	}
	for _, c := range s.ByCategory {
		if c.Category == "Сервисы" {
			t.Errorf("долларовая категория попала в рублёвую разбивку: %+v", c)
		}
		if c.Category == "Еда" && c.Total != domain.NewMoney(10000) {
			t.Errorf("Еда = %s, want 100.00 — доллары смешаны с рублями", c.Total)
		}
	}

	want := []finance.CurrencyTotal{
		{Currency: "USD", ExpenseCount: 2, Expenses: domain.NewMoney(1730)},
		{Currency: "UZS", IncomeCount: 1, Income: domain.NewMoney(50000000)},
	}
	if len(s.OtherCurrencies) != len(want) {
		t.Fatalf("OtherCurrencies = %+v, want %+v", s.OtherCurrencies, want)
	}
	for i := range want {
		if s.OtherCurrencies[i] != want[i] {
			t.Errorf("OtherCurrencies[%d] = %+v, want %+v", i, s.OtherCurrencies[i], want[i])
		}
	}
}

// Перевод себе в долларах — не рублёвый перевод. Сложить его с рублёвыми
// переводами значило бы сделать ровно то, чего итоги избегают.
func TestSummarize_transferInAnotherCurrencyIsNotARoubleTransfer(t *testing.T) {
	mk := func(code string) finance.Record {
		t.Helper()
		p := finance.AddParams{
			Kind: domain.KindExpense, Date: day(2026, time.October, 22), Amount: domain.NewMoney(100000),
			Category: "Прочее", Subcategory: "Перевод себе",
		}
		if code != "" {
			p.Currency = cur(t, code)
		}
		rec, err := finance.Add(p, func() string { return "01T" + code }, currencyNow)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		return rec
	}
	s := finance.Summarize([]finance.Record{mk(""), mk("USD")})
	if s.ExcludedTransfers != domain.NewMoney(100000) || s.ExcludedTransferCount != 1 {
		t.Errorf("рублёвые переводы = %s (%d), want 1000.00 (1)", s.ExcludedTransfers, s.ExcludedTransferCount)
	}
	if len(s.OtherCurrencies) != 1 || s.OtherCurrencies[0].TransferCount != 1 ||
		s.OtherCurrencies[0].Transfers != domain.NewMoney(100000) || s.OtherCurrencies[0].ExpenseCount != 0 {
		t.Errorf("OtherCurrencies = %+v, want USD transfer 1000.00 (1), no expenses", s.OtherCurrencies)
	}
}
