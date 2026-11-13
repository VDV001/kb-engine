package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeOzon answers like Seller API: один кабинет, Client-Id 42, ключ k-1.
func fakeOzon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "42" || r.Header.Get("Api-Key") != "k-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		item := `{"sku":11,"offer_id":"100_46","name":"Брюки тестовые","quantity":1,"price":{"amount":"1000","currency":"RUB"}}`
		switch r.URL.Path {
		case "/v3/posting/fbo/list":
			posts := strings.Repeat(`{"status":"delivered","products":[`+item+`]},`, 9) +
				strings.Repeat(`{"status":"cancelled","cancellation":{"cancel_reason":"Покупатель отказался при вручении: товар не подошел"},"products":[`+item+`]},`, 7)
			_, _ = w.Write([]byte(`{"has_next":false,"cursor":"","postings":[` + strings.TrimSuffix(posts, ",") + `]}`))
		case "/v3/product/info/list":
			_, _ = w.Write([]byte(`{"items":[{"id":901,"offer_id":"100_46","name":"Брюки тестовые"},{"id":903,"offer_id":"300_M","name":"Пижама тестовая"}]}`))
		case "/v4/posting/fbs/list":
			_, _ = w.Write([]byte(`{"has_next":false,"cursor":"","postings":[]}`))
		case "/v1/finance/accrual/by-day":
			// Одна продажа 12.11 — последний день окна; остальные дни пустые.
			var body struct{ Date string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			acc := ""
			if body.Date == "2026-11-12" {
				acc = `{"accrual_id":1,"date":"2026-11-12","total_amount":{"amount":"462.50","currency":"RUB"},"accrued_category":"POSTING","posting":{"products":[{"sku":11,"quantity":1,` +
					`"delivery":{"services":[{"type_id":32,"accrued":{"amount":"-150","currency":"RUB"}},{"type_id":29,"accrued":{"amount":"-11.50","currency":"RUB"}}]},` +
					`"commission":{"sale_amount":{"amount":"1200","currency":"RUB"},"commission":{"amount":"-576","currency":"RUB"}}}]}}`
			}
			_, _ = w.Write([]byte(`{"accruals":[` + acc + `],"last_id":""}`))
		case "/v1/returns/list":
			// Один невыкуп Ozon списал месяц назад, второй вернулся на склад.
			_, _ = w.Write([]byte(`{"has_next":false,"returns":[` +
				`{"id":1,"type":"Cancellation","posting_number":"2-1","product":{"sku":33,"name":"Пижама тестовая","price":{"currency_code":"RUB","price":2000}},"visual":{"status":{"sys_name":"WriteOff"},"change_moment":"2026-10-10T10:00:00Z"}},` +
				`{"id":2,"type":"Cancellation","posting_number":"2-2","product":{"sku":11,"name":"Брюки тестовые","price":{"currency_code":"RUB","price":1000}},"visual":{"status":{"sys_name":"ReturnedToOzon"},"change_moment":"2026-10-11T10:00:00Z"}}]}`))
		case "/v4/product/info/stocks":
			_, _ = w.Write([]byte(`{"cursor":"","items":[{"product_id":901,"offer_id":"100_46","stocks":[{"type":"fbo","present":0,"reserved":0,"sku":11}]},{"product_id":903,"offer_id":"300_M","stocks":[{"type":"fbo","present":40,"reserved":0,"sku":33}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Кабинет Ozon — подключение окна: Client-Id в config.json, ключ в Связке;
// «Магазин» читает его и дает советы.
func TestMarketsOzon(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if _, err := s.AddOzon("Тестовый магазин", "abc", "k-1", false); err == nil {
		t.Error("Client-Id of letters saved")
	}
	if _, err := s.AddOzon("Тестовый магазин", "42", " ", false); err == nil {
		t.Error("empty key saved")
	}
	row, err := s.AddOzon(" Тестовый магазин ", " 42 ", " k-1 ", false)
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "ozon" || row.Name != "Тестовый магазин" || !row.OK || !strings.Contains(row.Detail, "42") {
		t.Errorf("row = %+v", row)
	}
	if tok.m[connKeyAccount(row.ID)] != "k-1" {
		t.Errorf("key in keychain = %q", tok.m[connKeyAccount(row.ID)])
	}
	if c, _ := s.snapshot().cfg.Connection(row.ID); c.Account != "42" || strings.Contains(c.Name+c.URL+c.Model, "k-1") {
		t.Errorf("config holds %+v; the key must not be there", c)
	}

	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	v, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now)
	if err != nil {
		t.Fatalf("ozonShop: %v", err)
	}
	if v.Source != "ozon" || v.File != "Ozon · Тестовый магазин" || v.PeriodDays != 90 || v.Period != "15.08.2026 – 12.11.2026" || len(v.Products) != 2 {
		t.Fatalf("view = %+v", v)
	}
	if p := v.Products[0]; p.SKU != "11" || p.Article != "100_46" || p.Ordered != 16 || p.Refused != 7 || p.Refuse != 0.4375 || !p.Urgent {
		t.Errorf("trousers = %+v", p)
	}
	kinds := []string{}
	for _, a := range v.Advice {
		kinds = append(kinds, a.Kind)
	}
	if strings.Join(kinds, " ") != "refusals supply dead" || v.Advice[0].SKUs[0] != "11" || v.Advice[2].Units != 40 {
		t.Errorf("advice = %+v", v.Advice)
	}

	// Ключ не уходит ни в данные экрана, ни в текст ошибки (#580: ключи — не в логи и не модели).
	if raw, _ := json.Marshal(v); strings.Contains(string(raw), "k-1") {
		t.Error("the view carries the key")
	}
	tok.m[connKeyAccount(row.ID)] = "wrong-secret"
	if _, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now); err == nil || !strings.Contains(err.Error(), "не принял") || strings.Contains(err.Error(), "wrong-secret") {
		t.Errorf("wrong key: err = %v, want words about the key and not the key itself", err)
	}
	if _, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), "нет-такого", now); err == nil {
		t.Error("unknown connection read")
	}
}

// Начисления: «Магазин» кабинета показывает, сколько пришло на счет, строками
// «Экономики магазина»; отказ Ozon в финансах не прячет товары.
func TestMarketsOzonEconomy(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	v, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now)
	if err != nil {
		t.Fatalf("ozonShop: %v", err)
	}
	want := economyView{Sales: 1200, Commission: -576, Delivery: -150, Partners: -11.5, Total: 462.5}
	if v.Economy == nil || *v.Economy != want {
		t.Errorf("economy = %+v, want %+v", v.Economy, want)
	}
	// Пропажи: списанный невыкуп — проверить; вернувшийся на склад — нет.
	if l := v.Losses; l == nil || l.Err != "" || len(l.Rows) != 1 || l.Rows[0] != (lossRow{Kind: "write_off", Posting: "2-1", SKU: "33", Name: "Пижама тестовая", Price: 2000, Since: "10.10.2026"}) {
		t.Errorf("losses = %+v", v.Losses)
	}

	// Ключ без доступа к финансам: товары есть, у экономики — слова Ozon, без ключа.
	finance := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/finance/accrual/by-day" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	})
	refused := httptest.NewServer(finance)
	defer refused.Close()
	v, err = s.ozonShop(context.Background(), refused.URL, refused.Client(), row.ID, now)
	if err != nil {
		t.Fatalf("ozonShop without finance: %v", err)
	}
	// Тот же ключ только что прочитал товары: отказ в финансах — нет роли, а не неверный ключ (ревью #594).
	if e := v.Economy; len(v.Products) != 2 || e == nil || !strings.Contains(e.Err, "Финансы") || strings.Contains(e.Err, "не принял") || strings.Contains(e.Err, "k-1") || e.Total != 0 {
		t.Errorf("refused finance: products %d, economy %+v", len(v.Products), v.Economy)
	}
	// Начисления есть, возвраты закрыты: причина — про возвраты, а не про «Финансы» начислений (ревью #595).
	noReturns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/returns/list" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer noReturns.Close()
	if v, err := s.ozonShop(context.Background(), noReturns.URL, noReturns.Client(), row.ID, now); err != nil || v.Losses == nil ||
		!strings.Contains(v.Losses.Err, "возврат") || strings.Contains(v.Losses.Err, "начислени") || v.Economy == nil || v.Economy.Total != 462.5 {
		t.Errorf("returns refused: err %v, losses %+v, economy %+v", err, v.Losses, v.Economy)
	}
	// Без начислений «доставлено без продажи» не проверить: окно говорит об этом, а не молчит.
	if l := v.Losses; l == nil || !strings.Contains(l.Err, "начислени") {
		t.Errorf("losses without accruals = %+v, want the reason", v.Losses)
	}
}
