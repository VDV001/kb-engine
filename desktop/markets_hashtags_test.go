package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOzonCard answers /v4/product/info/attributes like Ozon Seller API (замер 15.11).
func fakeOzonCard(t *testing.T, tags string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "42" || r.Header.Get("Api-Key") != "k-1" || r.URL.Path != "/v4/product/info/attributes" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"result":[{"name":"Футболка женская","attributes":[{"id":23171,"values":[{"value":"` + tags + `"}]}]}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// «Хештеги» (#610): хештеги — из кабинета Ozon, ниша и запросы — из MPStats;
// второй разбор того же товара берет нишу из памяти, тариф не тратится.
func TestHashtagsView(t *testing.T) {
	s, mp := titleSettings(t, false, titleCard)
	var mcpCalls atomic.Int32
	mp.Config.Handler = countCalls(mp.Config.Handler, &mcpCalls)
	oz := fakeOzonCard(t, "#футболка_женская #футболка_мужская #zara_футболка")
	row, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, err := s.hashtagsCheck(ctx, mp.Client(), oz.URL, row.ID, "1234567", false)
	if err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, r := range v.Tags {
		why[r.Tag] = r.Why
	}
	if why["#футболка_женская"] != "ok" || why["#футболка_мужская"] != "gender" || why["#zara_футболка"] != "brand" || v.Main != "футболка женская" || len(v.Remove) != 2 {
		t.Fatalf("view %+v", v)
	}
	if len(v.Add) == 0 || v.Add[0].Why != "new" {
		t.Errorf("add %+v", v.Add)
	}
	before := mcpCalls.Load()
	if _, err := s.titleCheck(ctx, mp.Client(), "1234567", "Футболка женская", false); err != nil {
		t.Fatal(err)
	}
	if got := mcpCalls.Load(); got != before {
		t.Errorf("title check after hashtags asked MPStats %d more times, want the niche from memory", got-before)
	}
	if _, err := s.hashtagsCheck(ctx, mp.Client(), oz.URL, row.ID, "x", false); err == nil || !strings.Contains(err.Error(), "SKU") {
		t.Errorf("bad sku: %v", err)
	}
}

func countCalls(h http.Handler, n *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			n.Add(1)
		}
		h.ServeHTTP(w, r)
	})
}

// Ревью #625: ниша хранится по пути, а не по SKU — второй товар той же ниши
// не читает ее заново; «Проверить еще раз» (fresh) перечитывает; «брендов нет»
// — не сбой, и такая ниша тоже хранится.
func TestCardNicheCache(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var category atomic.Int32
	srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		switch {
		case name == "ozon_sku" && a["report"] == "full":
			return titleCard, false
		case name == "ozon_sku":
			return `[]`, false
		case a["report"] == "brands":
			category.Add(1)
			return "", false // MPStats: брендов нет — пустой ответ
		default:
			category.Add(1)
			return titleNicheJSON, false
		}
	})
	s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, sku := range []string{"1234567", "7654321"} {
		if _, err := s.titleCheck(ctx, srv.Client(), sku, "Футболка женская", false); err != nil {
			t.Fatal(err)
		}
	}
	if got := category.Load(); got != 2 {
		t.Errorf("niche reports asked %d times for two items of one niche, want 2 (queries and brands once)", got)
	}
	if _, err := s.titleCheck(ctx, srv.Client(), "1234567", "Футболка женская", true); err != nil {
		t.Fatal(err)
	}
	if got := category.Load(); got != 4 {
		t.Errorf("fresh check asked the niche %d times in all, want 4", got)
	}
}
