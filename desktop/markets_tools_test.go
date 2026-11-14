package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// Инструменты магазина в чате (#580): только у кабинетов, которым человек
// разрешил давать цифры модели (ревью #593); кабинет — по имени словами
// человека, иначе первый; кабинет читается один раз за ход.
func TestShopExtra(t *testing.T) {
	s, _, _ := newServerSettings(t)
	if x := shopExtras(s, "", nil, time.Now, nil); x != nil {
		t.Fatalf("no cabinets, extras = %v", x)
	}
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Header.Get("Api-Key")+" "+r.URL.Path]++
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
	if _, err := s.AddOzon("Брюки и худи", "1", "k-1", false); err != nil {
		t.Fatal(err)
	}
	if x := shopExtras(s, srv.URL, srv.Client(), time.Now, nil); x != nil {
		t.Fatal("a cabinet without the person's consent went to the model")
	}
	a, _ := s.AddOzon("Брюки и худи", "1", "k-1", true)
	if _, err := s.AddOzon("Брюки", "12", "k-2", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOzon("Брюки летние", "2", "k-3", true); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }
	x := shopExtras(s, srv.URL, srv.Client(), now, nil)
	if len(x) != 1 || !strings.Contains(x[0].Prompt(), "shop_") || len(x[0].Tools()) != 6 {
		t.Fatalf("extras = %v", x)
	}
	ask := func(args string) string {
		out, ok := x[0].Exec(context.Background(), "shop_summary", []byte(args))
		if !ok {
			t.Fatalf("shop_summary not run for %s", args)
		}
		return out
	}
	if out := ask(`{}`); !strings.Contains(out, "«Брюки и худи»") {
		t.Errorf("first shared cabinet by default: %q", out)
	}
	if out := ask(`{"cabinet":"брюки"}`); !strings.Contains(out, "«Брюки»") {
		t.Errorf("exact name first: %q", out)
	}
	if out := ask(`{"cabinet":"12"}`); !strings.Contains(out, "«Брюки»") {
		t.Errorf("exact Client-Id first: %q", out)
	}
	if out := ask(`{"cabinet":"брюк"}`); !strings.Contains(out, "несколько") {
		t.Errorf("ambiguous name must ask: %q", out)
	}
	if out := ask(`{"cabinet":"куртки"}`); !strings.Contains(out, "нет кабинета") {
		t.Errorf("unknown cabinet: %q", out)
	}
	if n := seen["k-1 /v4/product/info/stocks"]; n != 1 {
		t.Errorf("stocks read %d times in one turn, want once", n)
	}
	// Деньги и пропажи: начисления за 90 дней читаются один раз за ход, и только по просьбе модели.
	if n := seen["k-1 /v1/finance/accrual/by-day"]; n != 0 {
		t.Errorf("accruals read %d times before shop_money was asked", n)
	}
	for _, tool := range []string{"shop_money", "shop_losses"} {
		if out, ok := x[0].Exec(context.Background(), tool, []byte(`{}`)); !ok || !strings.Contains(out, "«Брюки и худи»") {
			t.Errorf("%s: %q", tool, out)
		}
	}
	if n := seen["k-1 /v1/finance/accrual/by-day"]; n != 90 {
		t.Errorf("accruals read %d days, want 90 once per turn", n)
	}
	if n := seen["k-1 /v1/returns/list"]; n != 1 {
		t.Errorf("returns read %d times, want once per turn", n)
	}
	if _, ok := x[0].Exec(context.Background(), "search", []byte(`{}`)); ok {
		t.Error("the shop answered a tool that is not its own")
	}
	// Кабинеты выключены посреди хода — ошибка словами, а не паника.
	if err := s.updateConfig(func(cfg *desktopconfig.Config) {
		for i := range cfg.Connections {
			cfg.Connections[i].Enabled = false
		}
	}); err != nil {
		t.Fatal(err)
	}
	fresh := shopExtras(s, srv.URL, srv.Client(), now, nil)
	if fresh != nil {
		t.Error("extras with every cabinet off")
	}
	if out, _ := x[0].Exec(context.Background(), "shop_top", []byte(`{"by":"ordered"}`)); !strings.Contains(out, "нет кабинета") {
		t.Errorf("cabinets gone mid-turn: %q", out)
	}
	_ = a
}

// Ключ без доступа к возвратам: shop_money отвечает деньгами, shop_losses —
// причиной; неудача помнится до конца хода, Ozon не спрашивают заново (ревью #597).
func TestShopExtra_returnsRefused(t *testing.T) {
	s, _, _ := newServerSettings(t)
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path]++
		switch r.URL.Path {
		case "/v1/returns/list":
			w.WriteHeader(http.StatusForbidden)
		case "/v1/finance/accrual/by-day":
			_, _ = w.Write([]byte(`{"accruals":[],"last_id":""}`))
		case "/v4/product/info/stocks":
			_, _ = w.Write([]byte(`{"cursor":"","items":[]}`))
		default:
			_, _ = w.Write([]byte(`{"has_next":false,"postings":[],"items":[]}`))
		}
	}))
	defer srv.Close()
	if _, err := s.AddOzon("Тест", "1", "k-1", true); err != nil {
		t.Fatal(err)
	}
	x := shopExtras(s, srv.URL, srv.Client(), func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }, nil)
	if out, _ := x[0].Exec(context.Background(), "shop_money", []byte(`{}`)); !strings.Contains(out, "на счет") {
		t.Errorf("shop_money without returns: %q", out)
	}
	if out, _ := x[0].Exec(context.Background(), "shop_losses", []byte(`{}`)); !strings.Contains(out, "возврат") || strings.Contains(out, "на счет") {
		t.Errorf("shop_losses without returns: %q", out)
	}
	_, _ = x[0].Exec(context.Background(), "shop_losses", []byte(`{}`))
	if seen["/v1/returns/list"] != 1 || seen["/v1/finance/accrual/by-day"] != 90 {
		t.Errorf("returns asked %d, accrual days %d: once per turn, failure included", seen["/v1/returns/list"], seen["/v1/finance/accrual/by-day"])
	}
}

// Шаг чата называет кабинет и вопрос словами человека (отзыв 14.11): «Ozon
// «Брюки»: карточка товара «100_46»», а не имя инструмента.
func TestShopExtra_describe(t *testing.T) {
	s, _, _ := newServerSettings(t)
	a, _ := s.AddOzon("Брюки и худи", "1", "k-1", true)
	if _, err := s.AddOzon("Платья", "2", "k-2", true); err != nil {
		t.Fatal(err)
	}
	x := shopExtras(s, "", nil, time.Now, nil)
	tests := []struct{ tool, args, id, name, what string }{
		{"shop_summary", ``, a.ID, "Ozon «Брюки и худи»", "сводка магазина"},
		{"shop_sku", `{"q":"100_46","cabinet":"Платья"}`, "", "Ozon «Платья»", "карточка товара «100_46»"},
		{"shop_money", `{}`, a.ID, "Ozon «Брюки и худи»", "деньги магазина"},
		{"shop_top", `{"by":"ordered"}`, a.ID, "Ozon «Брюки и худи»", "топ товаров"},
		{"shop_supply", `{}`, a.ID, "Ozon «Брюки и худи»", "что довезти"},
		{"shop_losses", `{}`, a.ID, "Ozon «Брюки и худи»", "возможные пропажи"},
		{"shop_sku", `{"q":"1","cabinet":"Нет такого"}`, "", "Ozon", "карточка товара «1»"},
	}
	for _, tt := range tests {
		u, ok := x[0].Describe(tt.tool, []byte(tt.args))
		if !ok || u.Kind != chat.UseConnection || u.Name != tt.name || u.What != tt.what || (tt.id != "" && u.ID != tt.id) {
			t.Errorf("%s %s: %+v, %v", tt.tool, tt.args, u, ok)
		}
	}
	if _, ok := x[0].Describe("search", nil); ok {
		t.Error("search described by the shop")
	}
}
