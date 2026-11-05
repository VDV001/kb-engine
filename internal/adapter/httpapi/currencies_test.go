package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
)

// /api/finances отдает деньги по валютам — то, что экран показывает главной
// строкой (решение владельца 05.11), — и итоги родов по валютам. Рублевая
// оценка по курсу счетов (free, groups) остается рядом как справка.
func TestFinancesPerCurrency(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC) }
	m := func(s string) domain.Money { v, _ := domain.ParseMoney(s); return v }
	day := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	usd, _ := domain.NewCurrency("USD")
	rate, _ := domain.NewRate(m("90"))
	sber, _ := domain.NewAccount("Сбербанк", m("1000"), day, now)
	wise, _ := domain.NewForeignAccount("Wise", m("100"), usd, rate, day, now)
	stash, _ := domain.NewAccount("Резерв → Вклад", m("5000"), day, now)
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{},
		bookFinance{httpapi.Finances{Accounts: []domain.Account{sber, wise, stash}}},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/finances", nil))
	var out struct {
		Currencies []map[string]string `json:"currencies"`
		Totals     []map[string]string `json:"totals"`
		Free       string              `json:"free"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	wantCur := []map[string]string{
		{"currency": "", "current": "6000.00", "free": "1000.00"},
		{"currency": "USD", "current": "100.00", "free": "100.00"},
	}
	if !reflect.DeepEqual(out.Currencies, wantCur) {
		t.Errorf("currencies %v\nwant %v", out.Currencies, wantCur)
	}
	wantTot := []map[string]string{
		{"group": "", "currency": "", "confirmed": "1000.00", "current": "1000.00"},
		{"group": "", "currency": "USD", "confirmed": "100.00", "current": "100.00"},
		{"group": "Резерв", "currency": "", "confirmed": "5000.00", "current": "5000.00"},
	}
	if !reflect.DeepEqual(out.Totals, wantTot) {
		t.Errorf("totals %v\nwant %v", out.Totals, wantTot)
	}
	// Справка по курсу счетов не пропала: 1000 + 100·90 = 10 000.
	if out.Free != "10000.00" {
		t.Errorf("free %q, want the rate estimate 10000.00 kept beside", out.Free)
	}
}
