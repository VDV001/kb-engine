package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
)

type txFinance struct {
	summaryFinance
	fin httpapi.Finances
}

func (f *txFinance) Finances() (httpapi.Finances, error) { return f.fin, nil }

// Запись в другой валюте уходит в витрину со своим кодом. Без него 17.30 USD
// в списке читается как 17.30 рубля.
func TestFinances_transactionCarriesItsCurrency(t *testing.T) {
	usd, _ := domain.NewCurrency("USD")
	mk := func(id string, c domain.Currency) domain.Transaction {
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: id, Kind: domain.KindExpense, Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			Amount: domain.NewMoney(1730), Category: "Сервисы", Currency: c,
			Now: func() time.Time { return time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC) },
		})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	fin := &txFinance{fin: httpapi.Finances{Transactions: []domain.Transaction{mk("01R", domain.Currency{}), mk("01U", usd)}}}
	rec := httptest.NewRecorder()
	serverWith(fin).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/finances", nil))
	var body struct {
		Transactions []map[string]any `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	got := map[string]any{}
	for _, tx := range body.Transactions {
		got[tx["id"].(string)] = tx["currency"]
	}
	if got["01U"] != "USD" {
		t.Errorf("долларовая запись: currency = %v, want USD", got["01U"])
	}
	if got["01R"] != nil {
		t.Errorf("рублёвая запись несёт currency = %v, want отсутствие поля", got["01R"])
	}
}
