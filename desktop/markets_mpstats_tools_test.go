package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "Ниши", "m-1")
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
	if out := run("mpstats_niche", `{"path":"Одежда/Мужская одежда"}`); !strings.Contains(out, "Брюки") || !strings.Contains(out, "150") || !strings.Contains(out, "2026-10-15") {
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
