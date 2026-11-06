package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Вторая сторона обмена — содержание записи (#432): правка «Получено» в книге
// обязана дойти до журнала, а сверка видит правку только через отпечаток.
// Запись без обмена хеширует как раньше — отпечатки в .sync-state.json
// владельца не меняются (TestFingerprint_roublesHashAsBefore).
func TestFingerprint_receivedIsContent(t *testing.T) {
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatal(err)
	}
	exchange := func(cents int64, account string) domain.Transaction {
		t.Helper()
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: "01A", Kind: domain.KindExpense, Date: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Amount: domain.NewMoney(900000), Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк",
			Received: domain.ReceivedParams{Amount: domain.NewMoney(cents), Currency: usd, Account: account},
			Now:      clock,
		})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	a := finance.Fingerprint(exchange(10000, "Wise"))
	if a == finance.Fingerprint(exchange(10500, "Wise")) {
		t.Error("смена полученной суммы обязана менять отпечаток")
	}
	if a == finance.Fingerprint(exchange(10000, "Банк USD")) {
		t.Error("смена счета получения обязана менять отпечаток")
	}
}
