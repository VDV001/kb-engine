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
	if _, err := s.AddOzon("FURIA", "abc", "k-1", false); err == nil {
		t.Error("Client-Id of letters saved")
	}
	if _, err := s.AddOzon("FURIA", "42", " ", false); err == nil {
		t.Error("empty key saved")
	}
	row, err := s.AddOzon(" FURIA ", " 42 ", " k-1 ", false)
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "ozon" || row.Name != "FURIA" || !row.OK || !strings.Contains(row.Detail, "42") {
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
	if v.Source != "ozon" || v.File != "Ozon · FURIA" || v.PeriodDays != 90 || v.Period != "15.08.2026 – 12.11.2026" || len(v.Products) != 2 {
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
