package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// titleMCP answers like mcp.mpstats.io: initialize → Mcp-Session-Id → tools/call;
// REST /api/user/report_api_limit — для проверки ключа при добавлении.
func titleMCP(t *testing.T, tool func(name string, args map[string]any) (string, bool)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/report_api_limit" {
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
			return
		}
		if r.URL.Path != "/mcp" || r.URL.Query().Get("token") != "m-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete { // закрытие сессии — не вызов инструмента
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		answer := func(result any) {
			w.Header().Set("Content-Type", "text/event-stream")
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			_, _ = io.WriteString(w, "data: "+string(b)+"\n\n")
		}
		switch {
		case req.Method == "initialize":
			w.Header().Set("Mcp-Session-Id", "s-1")
			answer(map[string]any{})
		case req.Method == "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case r.Header.Get("Mcp-Session-Id") != "s-1":
			w.WriteHeader(http.StatusBadRequest)
		default:
			text, isErr := tool(req.Params.Name, req.Params.Arguments)
			answer(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const titleNicheJSON = `{"queries":[` +
	`{"word":"лонгслив женский","count":150000,"items_count":1},` +
	`{"word":"футболка женская","count":110000,"items_count":780},` +
	`{"word":"футболка женская хлопок","count":8000,"items_count":300},` +
	`{"word":"футболка женская белая","count":9000,"items_count":300},` +
	`{"word":"футболка женская zara","count":9000,"items_count":300}],` +
	`"words":[{"word":"женская","words":["женская","женский"]}]}`

func titleSettings(t *testing.T, ownErr bool, card string) (*folderSettings, *httptest.Server) {
	t.Helper()
	s, _, _ := newServerSettings(t)
	srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		switch {
		case name == "ozon_sku" && a["report"] == "full":
			return card, false
		case name == "ozon_sku" && a["report"] == "keywords":
			if ownErr {
				return "Ошибка: сервис занят", true
			}
			return `[{"query":"футболка сиреневая","count":49,"avg_position":7}]`, false
		case name == "ozon_category" && a["report"] == "keywords" && a["path"] == "Одежда/Футболки женские":
			return titleNicheJSON, false
		case name == "ozon_category" && a["report"] == "brands" && a["path"] == "Одежда/Футболки женские":
			if ownErr {
				return "Ошибка: сервис занят", true
			}
			return `{"data":[{"name":"Бренд не указан"},{"name":"Zara"}]}`, false
		}
		return "wrong call", true
	})
	s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	return s, srv
}

const titleCard = `{"name":"Футболка 1 шт","niche":{"name":"Одежда/Футболки женские"},"brand":"","seller":{"name":"PRIMER"}}`

// «Название» (#607): ниша — из карточки товара в MPStats, главный запрос и
// советы — по запросам этой ниши, свои запросы товара — довод «оставить».
func TestTitleCheckView(t *testing.T) {
	s, srv := titleSettings(t, false, titleCard)
	v, err := s.titleCheck(context.Background(), srv.Client(), "1234567", "Футболка оверсайз сиреневая PRIMER", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Niche != "Одежда/Футболки женские" || v.Main != "футболка женская" || v.MainCount != 110000 || v.MainItems != 780 || v.Total != 5 || v.Related != 4 {
		t.Fatalf("view %+v", v)
	}
	if len(v.Add) == 0 || v.Add[0].Word != "женская" || v.Add[0].Why != "main" {
		t.Errorf("add %+v: the main query's word first", v.Add)
	}
	why := map[string]string{}
	for _, w := range v.Words {
		why[w.Word] = w.Why
	}
	if why["сиреневая"] != "attr" || why["primer"] != "brand" || why["оверсайз"] != "rare" {
		t.Errorf("words %+v", v.Words)
	}
	if len(v.Remove) != 1 || v.Remove[0].Word != "оверсайз" || v.Note != "" {
		t.Errorf("remove %+v note %q", v.Remove, v.Note)
	}
	if len(v.Dropped) != 1 || v.Dropped[0].Word != "zara" || v.Dropped[0].Why != "brand-niche" || v.NicheBrands != 1 {
		t.Errorf("dropped %+v of %d brands: Zara is a niche brand (#623)", v.Dropped, v.NicheBrands)
	}
}

// Свои запросы товара не прочитались — оценка по нише остается, окно говорит, чего нет.
func TestTitleCheckView_ownQueriesFail(t *testing.T) {
	s, srv := titleSettings(t, true, titleCard)
	v, err := s.titleCheck(context.Background(), srv.Client(), "1234567", "Футболка женская", false)
	if err != nil || v.Main != "футболка женская" || !strings.Contains(v.Note, "сервис занят") || !strings.Contains(v.Note, "бренд") {
		t.Fatalf("view %+v, %v: the niche check stays, the note names the miss", v, err)
	}
}

func TestTitleCheckView_refusals(t *testing.T) {
	s, srv := titleSettings(t, false, `{"name":"Футболка 1 шт","niche":{"name":""}}`)
	ctx := context.Background()
	for _, c := range []struct{ sku, title, want string }{
		{"ozon-1", "Футболка", "SKU"},
		{"1234567", " ", "название"},
		{"1234567", "Футболка", "ниши"},
	} {
		if _, err := s.titleCheck(ctx, srv.Client(), c.sku, c.title, false); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q: %v, want an error about %s", c.sku, c.title, err, c.want)
		}
	}
}

// У ниши нет запросов — это отказ с причиной, а не «добавить и убрать нечего» (ревью #621).
func TestTitleCheckView_emptyNiche(t *testing.T) {
	s, _, _ := newServerSettings(t)
	srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		if name == "ozon_category" {
			return `{"queries":[],"words":[]}`, false
		}
		if a["report"] == "full" {
			return titleCard, false
		}
		return `[]`, false
	})
	s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.titleCheck(context.Background(), srv.Client(), "1234567", "Футболка женская", false); err == nil || !strings.Contains(err.Error(), "нет запросов") {
		t.Errorf("empty niche: %v, want «нет запросов»", err)
	}
}

// Истекший срок — по-русски, а не «context deadline exceeded» (ревью #621).
func TestMPStatsMessage_deadline(t *testing.T) {
	if m := mpstatsMessage(context.DeadlineExceeded); strings.Contains(m, "deadline") {
		t.Errorf("message %q", m)
	}
}
