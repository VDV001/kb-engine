package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Период — как у MPStats: вчера по Москве и 30 дней до него, 31 день.
func TestSearchPeriod(t *testing.T) {
	now := time.Date(2026, 11, 17, 1, 30, 0, 0, time.UTC) // 04:30 МСК
	from, to := searchPeriod(now)
	if to.Format(time.DateOnly) != "2026-11-16" || from.Format(time.DateOnly) != "2026-10-17" {
		t.Errorf("%s..%s", from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	late := time.Date(2026, 11, 16, 22, 0, 0, 0, time.UTC) // уже 17.11 в Москве
	if _, to := searchPeriod(late); to.Format(time.DateOnly) != "2026-11-16" {
		t.Errorf("after midnight in Moscow: %s", to.Format(time.DateOnly))
	}
}

func TestPlacesView(t *testing.T) {
	var calls atomic.Int32
	s, _, _ := newServerSettings(t)
	srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		calls.Add(1)
		switch {
		case name == "ozon_sku" && a["report"] == "full":
			return titleCard, false
		case name == "ozon_sku" && a["report"] == "keywords" && a["sku"] == float64(1234567):
			// 31 место по дням, как в настоящем ответе (замер 17.11).
			pad := strings.Repeat(",null", 28)
			return `[{"query":"футболка оверсайз","count":40,"avg_position":12,"positions":[10,null,14` + pad + `]},` +
				`{"query":"футболка женская","count":900,"avg_position":30,"positions":[25,31,null` + pad + `]}]`, false
		case name == "ozon_sku" && a["report"] == "keywords":
			return "", false // товар не находят ни по одному запросу
		case name == "ozon_sku" && a["report"] == "search_stats":
			return `{"search_words_count":0,"search_words_count_previous":237,"search_position_avg":null,"search_position_avg_previous":17}`, false
		case name == "ozon_category":
			return titleNicheJSON, false
		}
		return "wrong call", true
	})
	s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	ctx, now := context.Background(), time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	v, err := s.placesCheck(ctx, srv.Client(), "1234567", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.From != "2026-10-17" || v.To != "2026-11-16" || v.Total != 2 || len(v.Rows) != 2 || v.Rows[0].Query != "футболка женская" {
		t.Fatalf("view %+v", v)
	}
	if r := v.Rows[0]; r.Last != 31 || r.LastDay != "2026-10-18" || r.Seen != 2 || r.Days != 31 || r.Avg != 30 {
		t.Errorf("row %+v", r)
	}
	if v.Calls != 1 || !strings.Contains(v.Source, "MPStats") {
		t.Errorf("calls %d, source %q — один запрос тарифа", v.Calls, v.Source)
	}
	// «Название» прочитало свои запросы товара — место берется из памяти.
	s2, _, _ := newServerSettings(t)
	s2.mpstatsURL, s2.mcpURL = srv.URL, srv.URL+"/mcp"
	if _, err := s2.addMPStats(ctx, checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.titleCheck(ctx, srv.Client(), "1234567", "Футболка женская", false); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	// «Название» кладет в память по настоящим часам — и спрашиваем по ним.
	if v, err := s2.placesCheck(ctx, srv.Client(), "1234567", false, time.Now()); err != nil || v.Calls != 0 || len(v.Rows) != 2 {
		t.Errorf("after title: %+v %v", v, err)
	}
	if calls.Load() != before {
		t.Errorf("places after title asked MPStats %d times, want memory", calls.Load()-before)
	}
	// Не находят ни по одному запросу — сравнение с прошлым периодом.
	e, err := s.placesCheck(ctx, srv.Client(), "7654321", false, now)
	if err != nil || len(e.Rows) != 0 || e.Rows == nil || e.Stats == nil || e.Stats.WordsBefore != 237 || e.Calls != 2 {
		t.Fatalf("empty: %+v %v", e, err)
	}
	if _, err := s.placesCheck(ctx, srv.Client(), "x", false, now); err == nil || !strings.Contains(err.Error(), "SKU") {
		t.Errorf("bad sku: %v", err)
	}
}

// Ревью #673: один кэш своих запросов для «Места», «Названия» и «Хештегов» —
// в обе стороны; ошибка search_stats видна, а не проглочена.
func TestPlacesShareOwnQueries(t *testing.T) {
	var keywords atomic.Int32
	s, _, _ := newServerSettings(t)
	srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		switch {
		case name == "ozon_sku" && a["report"] == "full":
			return titleCard, false
		case name == "ozon_sku" && a["report"] == "keywords" && a["sku"] == float64(1234567):
			keywords.Add(1)
			return `[{"query":"футболка женская","count":900,"avg_position":30,"positions":[25]}]`, false
		case name == "ozon_sku" && a["report"] == "keywords":
			return "", false
		case name == "ozon_sku" && a["report"] == "search_stats":
			return "Ошибка: лимит тарифа исчерпан", true
		case name == "ozon_category":
			return titleNicheJSON, false
		}
		return "wrong call", true
	})
	s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
	ctx := context.Background()
	if _, err := s.addMPStats(ctx, checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.placesCheck(ctx, srv.Client(), "1234567", false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.titleCheck(ctx, srv.Client(), "1234567", "Футболка женская", false); err != nil {
		t.Fatal(err)
	}
	if n := keywords.Load(); n != 1 {
		t.Errorf("keywords asked %d times for places then title, want 1", n)
	}
	// Свежее чтение «Места» видят и следующие проверки, а не старая запись.
	if _, err := s.placesCheck(ctx, srv.Client(), "1234567", true, now); err != nil {
		t.Fatal(err)
	}
	if v, err := s.placesCheck(ctx, srv.Client(), "1234567", false, now); err != nil || v.Calls != 0 || keywords.Load() != 2 {
		t.Errorf("after fresh: calls %d, keywords %d, %v", v.Calls, keywords.Load(), err)
	}
	e, err := s.placesCheck(ctx, srv.Client(), "7654321", false, now)
	if err != nil || e.Stats != nil || !strings.Contains(e.Note, "лимит") {
		t.Errorf("search_stats error: %+v %v — причина должна дойти до окна", e, err)
	}
}
