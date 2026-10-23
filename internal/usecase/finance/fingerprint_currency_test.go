package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Валюта — содержание записи: та же сумма в долларах вместо рублей — другая
// трата, и сверка, которая этого не видит, оставила бы книгу и журнал молча
// несогласными.
func TestFingerprint_currencyIsContent(t *testing.T) {
	base := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := domain.NewTransaction(domain.TransactionParams{
		ID:          "01A",
		Kind:        domain.KindExpense,
		Date:        time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(20245),
		Category:    "Еда",
		Description: "хлеб",
		Currency:    usd,
		Now:         clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finance.Fingerprint(base) == finance.Fingerprint(foreign) {
		t.Error("смена валюты обязана менять отпечаток")
	}
}

// Отпечаток рублёвой записи обязан остаться ровно тем, что лежит в
// .sync-state.json у владельца. Поменяйся он у всех записей разом — первая
// сверка после обновления объявила бы, что изменились обе стороны во всех
// строках, то есть конфликт по всей книге. Значение снято с кода до появления
// валюты.
func TestFingerprint_roublesHashAsBefore(t *testing.T) {
	const before = "268b0976353df45cd3b1a01e25fb16b2"
	if got := finance.Fingerprint(expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")); got != before {
		t.Errorf("Fingerprint рублёвой записи = %s, want %s (как до валюты)", got, before)
	}
}
