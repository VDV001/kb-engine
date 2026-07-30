package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// summaryFinance записывает, какой набор месяцев у него запросили: главное в
// обработчике — разобрать параметр и передать его дальше, а не посчитать самому.
type summaryFinance struct {
	fakeFinance
	askedMonths []string
	summary     finance.Summary
	summaryErr  error
}

func (f *summaryFinance) Summary(months []string) (finance.Summary, error) {
	f.askedMonths = months
	return f.summary, f.summaryErr
}

func serverWith(fin httpapi.Financier) http.Handler {
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fin,
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{CurrentVersion: "0.9.0"}, nil },
		httpapi.Documents{}, nil)
}

func getJSON(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %q)", path, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET %s: невалидный JSON: %v", path, err)
	}
	return out
}

func TestFinanceSummaryServesEveryCut(t *testing.T) {
	fin := &summaryFinance{}
	got := getJSON(t, serverWith(fin), "/api/finances/summary")

	// Каждый разрез обязан присутствовать в ответе — даже пустой. Отсутствующее
	// поле во фронте неотличимо от «нет данных», и график молча исчезнет.
	for _, key := range []string{
		"expenses", "income", "net", "expenseCount", "incomeCount",
		"byCategory", "bySubcategory", "byPlace", "bySource", "incomeBySource",
		"byMonth", "byDay",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("в ответе нет поля %q", key)
		}
	}
	// Суммы — строки, а не числа: копейки в int64, и float в JSON вернул бы
	// 89.98999999999999 вместо 89.99.
	for _, key := range []string{"expenses", "income", "net"} {
		if _, ok := got[key].(string); !ok {
			t.Errorf("поле %q имеет тип %T, want string", key, got[key])
		}
	}
	// Списки — массивы, а не null: null во фронте пришлось бы обходить ??[].
	for _, key := range []string{"byCategory", "bySubcategory", "byPlace", "bySource", "incomeBySource", "byMonth", "byDay"} {
		if _, ok := got[key].([]any); !ok {
			t.Errorf("поле %q имеет тип %T, want массив", key, got[key])
		}
	}
}

func TestFinanceSummaryPassesMonthsThrough(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"без параметра — пусто", "", nil},
		{"один месяц", "?months=2026-07", []string{"2026-07"}},
		{"несколько через запятую", "?months=2026-06,2026-07", []string{"2026-06", "2026-07"}},
		{"пробелы обрезаются", "?months=2026-06%20,%202026-07", []string{"2026-06", "2026-07"}},
		// Пустые элементы выбрасываются: иначе «2026-07,» превратится в набор с
		// пустой строкой, который не совпадёт ни с одной записью и молча отдаст
		// пустой отчёт вместо запрошенного месяца.
		{"пустые элементы выбрасываются", "?months=2026-07,,", []string{"2026-07"}},
		{"только запятые — как без параметра", "?months=,,", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fin := &summaryFinance{}
			getJSON(t, serverWith(fin), "/api/finances/summary"+tt.query)
			if !reflect.DeepEqual(fin.askedMonths, tt.want) {
				t.Errorf("порт спросили про %#v, want %#v", fin.askedMonths, tt.want)
			}
		})
	}
}

func TestFinanceSummaryWithoutLedgerIsEmptyNotAnError(t *testing.T) {
	// Развёртывание без леджера — валидное: остальной дашборд работает.
	got := getJSON(t, serverWith(nil), "/api/finances/summary")
	if got["expenses"] != "0.00" {
		t.Errorf("expenses = %v, want \"0.00\"", got["expenses"])
	}
	if arr, ok := got["byCategory"].([]any); !ok || len(arr) != 0 {
		t.Errorf("byCategory = %v, want пустой массив", got["byCategory"])
	}
}

func TestFinanceSummaryHidesTheLedgerPathOnFailure(t *testing.T) {
	fin := &summaryFinance{summaryErr: errors.New("open /Users/someone/finances/transactions.jsonl: permission denied")}
	rec := httptest.NewRecorder()
	serverWith(fin).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/finances/summary", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// Путь к личному финансовому файлу не отдаём тому, кто спросил.
	if strings.Contains(rec.Body.String(), "transactions.jsonl") || strings.Contains(rec.Body.String(), "/Users/") {
		t.Errorf("ответ содержит путь к леджеру: %q", rec.Body.String())
	}
}
