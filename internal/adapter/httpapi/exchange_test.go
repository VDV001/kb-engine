package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
)

// Обмен через API: вторая сторона — объект received, как в журнале.
func TestPostExchange(t *testing.T) {
	fw := &fakeFinWriter{}
	rec := send(finServer(fw), http.MethodPost, "/api/finances/transactions",
		`{"kind":"expense","amount":"9000","category":"Прочее","subcategory":"Перевод себе","account":"Сбербанк",
		  "received":{"amount":"100","currency":"USD","account":"Wise"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if fw.got.Received == nil || fw.got.Received.Amount != "100" || fw.got.Received.Currency != "USD" || fw.got.Received.Account != "Wise" {
		t.Fatalf("writer got %+v (received %+v)", fw.got, fw.got.Received)
	}
	if rec := send(finServer(fw), http.MethodPost, "/api/finances/transactions", `{"amount":"1"}`); rec.Code != http.StatusCreated || fw.got.Received != nil {
		t.Fatalf("a plain operation: %d, received %+v", rec.Code, fw.got.Received)
	}
	if rec := send(finServer(fw), http.MethodPost, "/api/finances/transactions", `{"amount":"1","received":{"amount":"1","rate":"90"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown field inside received: %d, want 400", rec.Code)
	}
}

// /api/finances показывает вторую сторону и курс, вычисленный из двух сумм.
func TestFinancesShowTheExchange(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) }
	usd, _ := domain.NewCurrency("USD")
	m := func(s string) domain.Money { v, _ := domain.ParseMoney(s); return v }
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: "01EXCH", Kind: "expense", Date: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), Amount: m("9000"),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк", Now: now,
		Received: domain.ReceivedParams{Amount: m("100"), Currency: usd, Account: "Wise"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, bookFinance{httpapi.Finances{Transactions: []domain.Transaction{tx}}},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/finances", nil))
	var out struct {
		Transactions []struct {
			Received *struct{ Amount, Currency, Account, Rate string } `json:"received"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Transactions) != 1 || out.Transactions[0].Received == nil {
		t.Fatalf("%s", rec.Body.String())
	}
	if r := *out.Transactions[0].Received; r.Amount != "100.00" || r.Currency != "USD" || r.Account != "Wise" || r.Rate != "90.00" {
		t.Fatalf("received %+v", r)
	}
	// Выгрузка Excel — те же сведения колонками.
	_, f := getBook(t, srv, "/api/finances/export.xlsx")
	ops := sheetRows(t, f, "Операции")
	head := strings.Join(ops[0], "|")
	line := strings.Join(ops[1], "|")
	for _, want := range []string{"Получено", "На счёт", "Курс"} {
		if !strings.Contains(head, want) {
			t.Errorf("Операции lacks %s: %v", want, ops[0])
		}
	}
	for _, want := range []string{"100", "USD", "Wise", "90"} {
		if !strings.Contains(line, want) {
			t.Errorf("the exchange row lacks %s: %v", want, ops[1])
		}
	}
}

// Курс валюты дешевле рубля показывается значащими цифрами, а не «0.01».
func TestRateOfACheapCurrencyKeepsItsDigits(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) }
	uzs, _ := domain.NewCurrency("UZS")
	m := func(s string) domain.Money { v, _ := domain.ParseMoney(s); return v }
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: "01EXCH", Kind: "expense", Date: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), Amount: m("10000"),
		Category: "Прочее", Subcategory: "Перевод себе", Account: "Сбербанк", Now: now,
		Received: domain.ReceivedParams{Amount: m("1333000"), Currency: uzs, Account: "Kapitalbank"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, bookFinance{httpapi.Finances{Transactions: []domain.Transaction{tx}}},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/finances", nil))
	if !strings.Contains(rec.Body.String(), `"rate":"0.007502"`) {
		t.Fatalf("the rate of a sum: %s", rec.Body.String())
	}
}
