package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Инструменты магазина в чате (#580): кабинет — по имени словами человека,
// иначе первый включенный; без кабинетов инструментов нет.
func TestShopExtra(t *testing.T) {
	s, _, _ := newServerSettings(t)
	if x := shopExtras(s, "", nil, time.Now); x != nil {
		t.Fatalf("no cabinets, extras = %v", x)
	}
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Header.Get("Api-Key")]++
		switch r.URL.Path {
		case "/v3/posting/fbo/list":
			_, _ = w.Write([]byte(`{"has_next":false,"postings":[{"status":"delivered","created_at":"2026-11-10T10:00:00Z","products":[{"sku":11,"offer_id":"100_46","name":"Брюки тестовые","quantity":1,"price":{"amount":"3000","currency":"RUB"}}]}]}`))
		case "/v3/product/info/list":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/v4/product/info/stocks":
			_, _ = w.Write([]byte(`{"cursor":"","items":[]}`))
		default:
			_, _ = w.Write([]byte(`{"has_next":false,"postings":[]}`))
		}
	}))
	defer srv.Close()
	if _, err := s.AddOzon("Пижамы", "1", "k-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOzon("Брюки и худи", "2", "k-2"); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }
	x := shopExtras(s, srv.URL, srv.Client(), now)
	if len(x) != 1 || !strings.Contains(x[0].Prompt(), "shop_") || len(x[0].Tools()) != 4 {
		t.Fatalf("extras = %v", x)
	}
	out, ok := x[0].Exec(context.Background(), "shop_summary", []byte(`{}`))
	if !ok || !strings.Contains(out, "«Пижамы»") || seen["k-1"] == 0 {
		t.Errorf("first cabinet by default: ok %v, %q, keys %v", ok, out, seen)
	}
	out, _ = x[0].Exec(context.Background(), "shop_summary", []byte(`{"cabinet":"брюки"}`))
	if !strings.Contains(out, "«Брюки и худи»") || seen["k-2"] == 0 {
		t.Errorf("cabinet by name: %q", out)
	}
	if _, ok := x[0].Exec(context.Background(), "search", []byte(`{}`)); ok {
		t.Error("the shop answered a tool that is not its own")
	}
	if out, ok := x[0].Exec(context.Background(), "shop_summary", []byte(`{"cabinet":"куртки"}`)); !ok || !strings.Contains(out, "нет кабинета") {
		t.Errorf("unknown cabinet: %q", out)
	}
}
