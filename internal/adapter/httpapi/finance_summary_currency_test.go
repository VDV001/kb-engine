package httpapi_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Записи в другой валюте едут вместе с суммами, как исключённые переводы: они
// объясняют, почему сумма строк журнала не сходится с рублёвыми расходами.
func TestFinanceSummaryCarriesOtherCurrencies(t *testing.T) {
	fin := &summaryFinance{summary: finance.Summary{
		OtherCurrencies: []finance.CurrencyTotal{
			{Currency: "USD", ExpenseCount: 2, Expenses: domain.NewMoney(1730)},
		},
	}}
	got := getJSON(t, serverWith(fin), "/api/finances/summary")
	list, ok := got["otherCurrencies"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("otherCurrencies = %#v, want one entry", got["otherCurrencies"])
	}
	row := list[0].(map[string]any)
	if row["currency"] != "USD" || row["expenses"] != "17.30" || row["expenseCount"] != float64(2) {
		t.Errorf("otherCurrencies[0] = %#v, want USD 17.30 (2)", row)
	}
}
