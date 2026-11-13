package ozon_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// accrualServer answers /v1/finance/accrual/by-day like Ozon: день строкой,
// страницы по last_id (пустой — конец), суммы строками.
func accrualServer(t *testing.T, days map[string][]string, asked *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/finance/accrual/by-day" || r.Header.Get("Api-Key") != "secret" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Date   string `json:"date"`
			LastID string `json:"last_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":3,"message":"invalid value for string field date"}`))
			return
		}
		*asked = append(*asked, body.Date+"/"+body.LastID)
		pages := days[body.Date]
		i := 0
		if body.LastID != "" {
			i = int(body.LastID[0] - '0')
		}
		if i >= len(pages) {
			_, _ = w.Write([]byte(`{"accruals":[],"last_id":""}`))
			return
		}
		_, _ = w.Write([]byte(pages[i]))
	}))
}

func TestClient_accruals(t *testing.T) {
	const sale = `{"accrual_id":1,"date":"2026-09-01","total_amount":{"amount":"462.50","currency":"RUB"},"unit_number":"1-1-1","accrued_category":"POSTING",
		"posting":{"delivery_schema":"Fbo","products":[{"sku":11,"quantity":1,
			"delivery":{"total_accrued":{"amount":"-161.50","currency":"RUB"},"services":[{"type_id":32,"accrued":{"amount":"-150","currency":"RUB"}},{"type_id":29,"accrued":{"amount":"-11.50","currency":"RUB"}}]},
			"commission":{"seller_price":{"amount":"1200","currency":"RUB"},"sale_price":{"amount":"500","currency":"RUB"},"sale_commission":{"amount":"-576","currency":"RUB"},
				"commission":{"amount":"-576","currency":"RUB"},"commission_ratio":"value:\"0.480000\"","sale_amount":{"amount":"1200","currency":"RUB"},
				"coinvestment":{"amount":"0","currency":"RUB"},"bonus":{"amount":"700","currency":"RUB"}}}]},
		"item_fees":null,"non_item_fee":null,"container_fees":null}`
	const item = `{"accrual_id":2,"date":"2026-09-01","total_amount":{"amount":"-15","currency":"RUB"},"unit_number":"1-1-1","accrued_category":"ITEM","posting":null,
		"item_fees":{"fees":[{"sku":11,"quantity":1,"fees":[{"type_id":38,"accrued":{"amount":"-5","currency":"RUB"}},{"type_id":39,"accrued":{"amount":"-10","currency":"RUB"}}]}]},"non_item_fee":null,"container_fees":null}`
	// Итог Ozon не равен сумме частей: разница — отдельной строкой, «на счет» остается итогом Ozon.
	const nonItem = `{"accrual_id":3,"date":"2026-09-02","total_amount":{"amount":"-700.45","currency":"RUB"},"unit_number":"9001","accrued_category":"NON_ITEM","posting":null,
		"item_fees":null,"non_item_fee":{"type_id":41,"accrued":{"amount":"-700.40","currency":"RUB"}},"container_fees":null}`
	var asked []string
	srv := accrualServer(t, map[string][]string{
		"2026-09-01": {`{"accruals":[` + sale + `],"last_id":"1"}`, `{"accruals":[` + item + `],"last_id":""}`},
		"2026-09-02": {`{"accruals":[` + nonItem + `],"last_id":""}`},
	}, &asked)
	defer srv.Close()
	c := ozon.Client{BaseURL: srv.URL, ClientID: "42", Key: "secret", HTTP: srv.Client()}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	acc, err := c.Accruals(context.Background(), from, from.AddDate(0, 0, 2))
	if err != nil {
		t.Fatalf("Accruals: %v", err)
	}
	if want := []string{"2026-09-01/", "2026-09-01/1", "2026-09-02/", "2026-09-03/"}; !slices.Equal(asked, want) {
		t.Errorf("asked = %v, want %v: every day, every page by last_id", asked, want)
	}
	k := func(kop int64) domain.Money { return domain.NewMoney(kop) }
	d1, d2 := from, from.AddDate(0, 0, 1)
	// Номер отправления — у начислений по отправлению и по товару; у рекламы его нет.
	want := []markets.OzonAccrual{
		{Day: d1, SKU: "11", Unit: "1-1-1", Kind: markets.AccrualSale, Amount: k(120000)},
		{Day: d1, SKU: "11", Unit: "1-1-1", Kind: markets.AccrualCommission, Amount: k(-57600)},
		{Day: d1, SKU: "11", Unit: "1-1-1", Kind: markets.AccrualService, TypeID: 32, Amount: k(-15000)},
		{Day: d1, SKU: "11", Unit: "1-1-1", Kind: markets.AccrualService, TypeID: 29, Amount: k(-1150)},
		{Day: d1, SKU: "11", Unit: "1-1-1", Kind: markets.AccrualService, TypeID: 38, Amount: k(-500)},
		{Day: d1, SKU: "11", Unit: "1-1-1", Kind: markets.AccrualService, TypeID: 39, Amount: k(-1000)},
		{Day: d2, Kind: markets.AccrualService, TypeID: 41, Amount: k(-70040)},
		{Day: d2, Kind: markets.AccrualRest, Amount: k(-5)},
	}
	if !slices.Equal(acc, want) {
		t.Errorf("accruals = %+v\nwant %+v", acc, want)
	}
}

func TestClient_accrualsToleratesMissingPartsAndZeroPadding(t *testing.T) {
	// Нет поля вознаграждения и услуга с accrued:null — часть без суммы, а не
	// отказ за все 90 дней; лишние нули дроби ("-11.5000") — те же копейки.
	const odd = `{"accrual_id":7,"date":"2026-09-01","total_amount":{"amount":"1188.5000","currency":"RUB"},"accrued_category":"POSTING",
		"posting":{"products":[{"sku":11,"commission":{"sale_amount":{"amount":"1200.00","currency":"RUB"}},
			"delivery":{"services":[{"type_id":29,"accrued":{"amount":"-11.5000","currency":"RUB"}},{"type_id":32,"accrued":null}]}}]}}`
	var asked []string
	srv := accrualServer(t, map[string][]string{"2026-09-01": {`{"accruals":[` + odd + `],"last_id":""}`}}, &asked)
	defer srv.Close()
	c := ozon.Client{BaseURL: srv.URL, ClientID: "42", Key: "secret", HTTP: srv.Client()}
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	acc, err := c.Accruals(context.Background(), day, day)
	if err != nil {
		t.Fatalf("Accruals: %v", err)
	}
	want := []markets.OzonAccrual{
		{Day: day, SKU: "11", Kind: markets.AccrualSale, Amount: domain.NewMoney(120000)},
		{Day: day, SKU: "11", Kind: markets.AccrualService, TypeID: 29, Amount: domain.NewMoney(-1150)},
	}
	if !slices.Equal(acc, want) {
		t.Errorf("accruals = %+v\nwant %+v", acc, want)
	}
}

func TestClient_accrualsRefusesOtherCurrency(t *testing.T) {
	// Валюты окно не смешивает: рубли с долларами в одной сумме — неверное «на счет».
	const usd = `{"accrual_id":9,"date":"2026-09-01","total_amount":{"amount":"-5","currency":"USD"},"accrued_category":"NON_ITEM","posting":null,"item_fees":null,
		"non_item_fee":{"type_id":41,"accrued":{"amount":"-5","currency":"USD"}},"container_fees":null}`
	var asked []string
	srv := accrualServer(t, map[string][]string{"2026-09-01": {`{"accruals":[` + usd + `],"last_id":""}`}}, &asked)
	defer srv.Close()
	c := ozon.Client{BaseURL: srv.URL, ClientID: "42", Key: "secret", HTTP: srv.Client()}
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.Accruals(context.Background(), day, day); err == nil {
		t.Error("an accrual in USD must be refused, not summed with rubles")
	}
}

func TestClient_returns(t *testing.T) {
	// Как /v1/returns/list: страницы по last_id (id последнего), has_next, цена объектом.
	var asked []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Filter struct {
				Date struct{ TimeFrom, TimeTo string } `json:"logistic_return_date"`
			} `json:"filter"`
			Limit  int   `json:"limit"`
			LastID int64 `json:"last_id"`
		}
		if r.URL.Path != "/v1/returns/list" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Limit > 500 || body.Filter.Date.TimeFrom == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		asked = append(asked, body.LastID)
		ret := func(id int64, n, st, changed string) string {
			return `{"id":` + strconv.FormatInt(id, 10) + `,"type":"Cancellation","posting_number":"` + n + `","product":{"sku":22,"name":"Пижама тестовая","price":{"currency_code":"RUB","price":2000}},` +
				`"visual":{"status":{"id":1,"display_name":"…","sys_name":"` + st + `"},"change_moment":"` + changed + `"}}`
		}
		if body.LastID == 0 {
			_, _ = w.Write([]byte(`{"returns":[` + ret(101, "2-1", "WriteOff", "2026-10-01T10:00:00Z") + `],"has_next":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"returns":[` + ret(102, "2-2", "ReturnedToOzon", "2026-10-02T10:00:00Z") + `],"has_next":false}`))
	}))
	defer srv.Close()
	c := ozon.Client{BaseURL: srv.URL, ClientID: "42", Key: "secret", HTTP: srv.Client()}
	from := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	rets, err := c.Returns(context.Background(), from, from.AddDate(0, 0, 89))
	if err != nil {
		t.Fatalf("Returns: %v", err)
	}
	if !slices.Equal(asked, []int64{0, 101}) {
		t.Errorf("asked last_id %v, want [0 101]", asked)
	}
	want := markets.OzonReturn{Posting: "2-1", SKU: "22", Name: "Пижама тестовая", Type: "Cancellation", Status: "WriteOff", Changed: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), Price: 2000}
	if len(rets) != 2 || rets[0] != want || rets[1].Status != "ReturnedToOzon" {
		t.Errorf("returns = %+v", rets)
	}
}
