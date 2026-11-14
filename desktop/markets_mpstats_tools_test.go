package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// fakeMPStatsChat answers the paths the chat tools ask.
func fakeMPStatsChat(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/categories":
			_, _ = w.Write([]byte(`[{"path":"Одежда/Мужская одежда/Брюки"},{"path":"Одежда/Женская одежда/Брюки"},{"path":"Обувь/Кеды"}]`))
		case "/api/oz/get/category/subcategories":
			_, _ = w.Write([]byte(`[{"name":"Текущая категория","sales":1},{"name":"Брюки","sales":150,"revenue":3000,"sellers_with_sells":7}]`))
		case "/api/oz/get/category/sellers":
			_, _ = w.Write([]byte(`[{"revenue":1500}]`))
		case "/api/oz/get/category/by_date":
			_, _ = w.Write([]byte(`[{"period":"2026-11-02","sales":10},{"period":"2026-11-03","sales":5},{"period":"2026-11-09","sales":2}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// MPStats в чате (отзыв 14.11: «не видит наши подключения в mpstats»): только
// с разрешения человека, как кабинет Ozon; шаг называет подключение и нишу.
func TestMPStatsExtra(t *testing.T) {
	s, _, _ := newServerSettings(t)
	srv := fakeMPStatsChat(t)
	s.mpstatsURL = srv.URL
	now := func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "Ниши", "m-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if x := mpstatsExtras(s, srv.Client(), now, nil); x != nil {
		t.Fatal("MPStats without the person's consent went to the model")
	}
	if err := s.SetOzonShare(row.ID, true); err != nil {
		t.Fatalf("share MPStats: %v", err)
	}
	if x := mpstatsExtras(s, srv.Client(), now, []string{"folder-1"}); x != nil {
		t.Fatal("MPStats switched off for the chat still went to the model")
	}
	x := mpstatsExtras(s, srv.Client(), now, nil)
	if len(x) != 1 || len(x[0].Tools()) != 3 || !strings.Contains(x[0].Prompt(), "mpstats_") {
		t.Fatalf("extras = %v", x)
	}
	u, ok := x[0].Describe("mpstats_niche", []byte(`{"path":"Одежда/Мужская одежда"}`))
	if !ok || u != (chat.Use{Kind: chat.UseConnection, ID: row.ID, Name: "MPStats «Ниши»", What: "ниши «Одежда/Мужская одежда»"}) {
		t.Errorf("describe = %+v %v", u, ok)
	}
	run := func(tool, args string) string {
		out, ok := x[0].Exec(context.Background(), tool, []byte(args))
		if !ok {
			t.Fatalf("%s not run", tool)
		}
		return out
	}
	if out := run("mpstats_categories", `{"q":"брюки муж"}`); !strings.Contains(out, "Одежда/Мужская одежда/Брюки") || strings.Contains(out, "Женская") {
		t.Errorf("categories = %q", out)
	}
	if out := run("mpstats_niche", `{"path":"Одежда/Мужская одежда"}`); !strings.Contains(out, "Брюки: Продажи, шт/мес 152") || !strings.Contains(out, "оценка") || !strings.Contains(out, "2026-10-15") {
		t.Errorf("niche = %q", out)
	}
	// Недели с понедельника: 02.11 — 15 шт, 09.11 — 2 шт.
	if out := run("mpstats_trend", `{"path":"Одежда/Мужская одежда/Брюки","weeks":2}`); !strings.Contains(out, "2026-11-02: 15") || !strings.Contains(out, "2026-11-09: 2") {
		t.Errorf("trend = %q", out)
	}
	if out := run("mpstats_niche", `{}`); !strings.Contains(out, "path") {
		t.Errorf("no path = %q", out)
	}
	if _, ok := x[0].Exec(context.Background(), "shop_summary", nil); ok {
		t.Error("shop tool run by MPStats")
	}
}

// Разрешение чату видно в «Подключениях» и в ряду «ищет в» (живая проверка
// 14.11: MPStats отвечал в чате, а плашка оставалась «запрещено»).
func TestMPStatsShareInRow(t *testing.T) {
	s, _, _ := newServerSettings(t)
	srv := fakeMPStatsChat(t)
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "Ниши", "m-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOzonShare(row.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Connections() {
		if r.ID == row.ID && !r.Share {
			t.Errorf("row = %+v; want share", r)
		}
	}
}

// Разрешение чату ставится сразу при добавлении, как у кабинета Ozon (отзыв
// 14.11: «в подключениях для mpstats тоже надо эту галочку»); по умолчанию нет.
func TestAddMPStatsWithShare(t *testing.T) {
	s, _, _ := newServerSettings(t)
	srv := fakeMPStatsChat(t)
	ep := checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}
	on, err := s.addMPStats(context.Background(), ep, "С чатом", "m-1", true)
	if err != nil {
		t.Fatal(err)
	}
	off, err := s.addMPStats(context.Background(), ep, "Без чата", "m-2", false)
	if err != nil {
		t.Fatal(err)
	}
	if !on.Share || off.Share {
		t.Errorf("share: on %v, off %v", on.Share, off.Share)
	}
}

// Сезон ниш MPStats догружается вторым вызовом: год по месяцам у каждой
// ниши, 12 полных месяцев до текущего (живая проверка 14.11).
func TestNichesMPStatsSeason(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/category/subcategories":
			_, _ = w.Write([]byte(`[{"name":"Брюки","sales":150,"revenue":3000,"sellers_with_sells":7}]`))
		case "/api/oz/get/category/sellers":
			_, _ = w.Write([]byte(`[{"revenue":1500}]`))
		case "/api/oz/get/category/by_date":
			asked = append(asked, q.Get("path")+" "+q.Get("groupBy")+" "+q.Get("d1")+"…"+q.Get("d2"))
			_, _ = w.Write([]byte(`[{"period":"2026-10-01","sales":40},{"period":"2025-11-01","sales":90}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	v, err := s.nichesMPStatsSeason(context.Background(), srv.Client(), "Одежда", "2026-10-15", "2026-11-13", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "Одежда/Брюки month 2025-11-01…2026-10-31" {
		t.Errorf("asked %v", asked)
	}
	if len(v.Niches) != 1 || len(v.Niches[0].Months) != 2 || v.Niches[0].Months[0].Label != "ноя 2025" {
		t.Errorf("niches = %+v", v.Niches)
	}
}

// Пустая категория — верхние категории Ozon с цифрами (отзыв 14.11: «хочу
// загрузить все что есть, а потом отфильтровать»): у каждой — ее собственная
// строка «Текущая категория», сейчас и год назад.
func TestNichesMPStatsTop(t *testing.T) {
	s, _, _ := newServerSettings(t)
	cats := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/categories":
			cats++
			_, _ = w.Write([]byte(`[{"path":"Одежда/Брюки"},{"path":"Одежда/Куртки"},{"path":"Обувь/Кеды"}]`))
		case "/api/oz/get/category/subcategories":
			sales := map[string]int{"Одежда": 300, "Обувь": 100}[q.Get("path")]
			if q.Get("d1") == "2025-10-15" {
				sales /= 2
			}
			_, _ = w.Write(fmt.Appendf(nil, `[{"name":"Текущая категория","sales":%d,"revenue":%d,"sellers_with_sells":5},{"name":"Брюки","sales":1}]`, sales, sales*10))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	v, err := s.nichesMPStats(context.Background(), srv.Client(), "", "2026-10-15", "2026-11-13", time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if v.File != "MPStats · все категории" || len(v.Niches) != 2 || v.Niches[0].Name != "Одежда" || v.Niches[0].Values["growth"] != 1 || v.Niches[1].Name != "Обувь" {
		t.Errorf("top = %+v", v)
	}
	// Повтор за те же дни — из памяти: 34 категории MPStats грузит 3-4 минуты (замер 14.11).
	if _, err := s.nichesMPStats(context.Background(), srv.Client(), "", "2026-10-15", "2026-11-13", time.Date(2026, 11, 14, 0, 5, 0, 0, time.UTC)); err != nil || cats != 1 {
		t.Errorf("second read: %v, categories asked %d times", err, cats)
	}
}

// Догрузка сезона не повторяет запросы первого показа (ревью 14.11): подкатегории
// за два года и топ-10 берутся из памяти, новые запросы — только месяцы.
func TestNichesMPStatsSeasonReusesTable(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var mu sync.Mutex
	asked := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/category/subcategories":
			_, _ = w.Write([]byte(`[{"name":"Брюки","sales":150,"revenue":3000,"sellers_with_sells":7}]`))
		case "/api/oz/get/category/sellers":
			_, _ = w.Write([]byte(`[{"revenue":1500}]`))
		case "/api/oz/get/category/by_date":
			_, _ = w.Write([]byte(`[{"period":"2026-10-01","sales":40}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if _, err := s.nichesMPStats(context.Background(), srv.Client(), "Одежда", "2026-10-15", "2026-11-13", now); err != nil {
		t.Fatal(err)
	}
	v, err := s.nichesMPStatsSeason(context.Background(), srv.Client(), "Одежда", "2026-10-15", "2026-11-13", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := asked["/api/oz/get/category/subcategories"]; got != 2 {
		t.Errorf("subcategories asked %d times, want 2 (now and a year ago, once)", got)
	}
	if got := asked["/api/oz/get/category/sellers"]; got != 1 {
		t.Errorf("sellers asked %d times, want 1", got)
	}
	if len(v.Niches) != 1 || len(v.Niches[0].Months) != 1 || len(v.Problems) != 0 {
		t.Errorf("season table lost the first show: %+v", v.Niches)
	}
}

// Верхняя категория без подкатегорий — сама ниша; топ-10 спрашивается по ее
// пути без «/» в начале, как и сезон (ревью 14.11: молча пустая доля).
func TestNichesMPStatsTopLeafPath(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var mu sync.Mutex
	var sellers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/category/subcategories":
			_, _ = w.Write([]byte(`[{"name":"Текущая категория","sales":90,"revenue":5000,"sellers_with_sells":4}]`))
		case "/api/oz/get/category/sellers":
			mu.Lock()
			sellers = append(sellers, r.URL.Query().Get("path"))
			mu.Unlock()
			_, _ = w.Write([]byte(`[{"revenue":1500}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	v, err := s.nichesMPStats(context.Background(), srv.Client(), "Книги", "2026-10-15", "2026-11-13", time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(sellers) != 1 || sellers[0] != "Книги" {
		t.Errorf("top-10 asked for %q, want [Книги]", sellers)
	}
	if len(v.Niches) != 1 || v.Niches[0].Name != "Книги" {
		t.Errorf("niches = %+v", v.Niches)
	}
}
