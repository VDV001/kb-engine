package mpstats_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
)

// fakeMCP answers like mcp.mpstats.io (живой замер 14.11): ключ в ?token=,
// initialize выдает Mcp-Session-Id, без него tools/call — отказ; ответ — SSE.
func fakeMCP(t *testing.T, tools map[string]func(args map[string]any) (text string, isErr bool)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "t-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete { // закрытие сессии: tools["DELETE"] видит, какой
			if fn, ok := tools["DELETE"]; ok {
				fn(map[string]any{"session": r.Header.Get("Mcp-Session-Id")})
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &req) != nil || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sse := func(v any) {
			w.Header().Set("Content-Type", "text/event-stream")
			b, _ := json.Marshal(v)
			_, _ = io.WriteString(w, "event: message\ndata: "+string(b)+"\n\n")
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s-1")
			sse(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": "2025-06-18"}})
			return
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "s-1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"session_token_mismatch","message":"Mcp-Session-Id required"}`)
			return
		}
		fn, ok := tools[req.Params.Name]
		if req.Method != "tools/call" || !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		text, isErr := fn(req.Params.Arguments)
		if text == "HTTP504" { // MPStats занят
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		content := []any{}
		if text != "" {
			content = append(content, map[string]any{"type": "text", "text": text})
		}
		sse(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": content, "isError": isErr}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCP_card(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_sku": func(a map[string]any) (string, bool) {
			if a["sku"] != float64(1234567) || a["report"] != "full" {
				return "wrong arguments", true
			}
			return `{"id":1234567,"name":"Футболка женская с рисунком 1 шт","niche":{"id":7508,"name":"Одежда/Женская одежда/Футболки и поло женские"},"brand":"","seller":{"id":1,"name":"PRIMER"}}`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	c, err := m.Card(context.Background(), 1234567)
	if err != nil || c.Niche != "Одежда/Женская одежда/Футболки и поло женские" || c.Seller != "PRIMER" || c.Brand != "" {
		t.Fatalf("card = %+v, %v", c, err)
	}
}

func TestMCP_queries(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_category": func(a map[string]any) (string, bool) {
			if a["path"] != "Одежда/Футболки" || a["report"] != "keywords" {
				return "wrong arguments", true
			}
			return `{"queries":[{"word":"футболка женская","count":111685,"wb_count":724426,"items_count":778,"efficiency":143.55}],` +
				`"words":[{"count":1406,"word":"женская","words":["женская","женский"],"keys":[]}]}`, false
		},
		"ozon_sku": func(a map[string]any) (string, bool) {
			if a["report"] != "keywords" || a["d1"] != "2026-10-17" || a["d2"] != "2026-11-16" {
				return "wrong arguments", true
			}
			return `[{"query":"сиреневая футболка женская","count":49,"total":0,"avg_position":7,"positions":[7,null,3]}]`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	qs, ws, err := m.NicheQueries(context.Background(), "Одежда/Футболки")
	if err != nil || len(qs) != 1 || qs[0].Text != "футболка женская" || qs[0].Count != 111685 || qs[0].Items != 778 ||
		len(ws) != 1 || ws[0].Word != "женская" || len(ws[0].Forms) != 2 {
		t.Fatalf("niche queries = %+v %+v, %v", qs, ws, err)
	}
	from, to := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	own, err := m.CardQueries(context.Background(), 1234567, from, to)
	if err != nil || len(own) != 1 || own[0].Text != "сиреневая футболка женская" || own[0].Position != 7 || own[0].Count != 49 {
		t.Fatalf("card queries = %+v, %v", own, err)
	}
	// Место по дням (замер 17.11: 31 значение на d1..d2, null — не в выдаче).
	if d := own[0].Days; len(d) != 3 || d[0] != 7 || d[1] != 0 || d[2] != 3 {
		t.Errorf("days %v", own[0].Days)
	}
}

func TestMCP_refusals(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_sku": func(a map[string]any) (string, bool) {
			switch a["sku"] {
			case float64(1):
				return "", false // MPStats не знает товара: пустой ответ
			case float64(7):
				return "Ошибка: сервис занят", true
			}
			return "Ошибка: sku not_found", true
		},
	})
	ctx := context.Background()
	if _, err := (mpstats.MCP{URL: srv.URL, Token: "чужой", HTTP: srv.Client()}).Card(ctx, 5); !errors.Is(err, mpstats.ErrUnauthorized) {
		t.Errorf("wrong token: %v, want ErrUnauthorized", err)
	}
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	if _, err := m.Card(ctx, 1); !errors.Is(err, mpstats.ErrNotFound) {
		t.Errorf("empty answer: %v, want ErrNotFound", err)
	}
	if _, err := m.Card(ctx, 5); !errors.Is(err, mpstats.ErrNotFound) {
		t.Errorf("tool error not_found: %v, want ErrNotFound (ревью #621)", err)
	}
	_, err := m.Card(ctx, 7)
	if err == nil || !strings.Contains(err.Error(), "сервис занят") || strings.Contains(err.Error(), "MPStats: MPStats") || strings.Contains(err.Error(), "t-1") {
		t.Errorf("tool error: %v, want its text once and no token", err)
	}
	if _, err := (mpstats.MCP{URL: "http://mcp.example/mcp", Token: "t-1"}).Card(ctx, 5); !errors.Is(err, mpstats.ErrInsecureURL) {
		t.Errorf("http: %v, want ErrInsecureURL (ключ — только по https)", err)
	}
}

// Занятый MPStats (504) повторяется, как в REST; сессия закрывается после вызова (ревью #621).
func TestMCP_retryAndClose(t *testing.T) {
	var calls, closed int
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_sku": func(map[string]any) (string, bool) {
			if calls++; calls == 1 {
				return "HTTP504", false
			}
			return `{"niche":{"name":"Одежда/Футболки"}}`, false
		},
		"DELETE": func(a map[string]any) (string, bool) {
			if a["session"] == "s-1" {
				closed++
			}
			return "", false
		},
	})
	c, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.Card(context.Background(), 5)
	if err != nil || c.Niche != "Одежда/Футболки" || calls != 2 {
		t.Fatalf("card %+v, %v after %d calls, want the 504 retried", c, err, calls)
	}
	if closed != 2 {
		t.Errorf("closed %d sessions, want each of the 2 closed", closed)
	}
}

// Ответ больше предела — так и сказано, а не «MPStats не прислал ответа» (ревью #621).
func TestMCP_tooLarge(t *testing.T) {
	big := `{"niche":{"name":"` + strings.Repeat("я", 9<<20) + `"}}`
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_sku": func(map[string]any) (string, bool) { return big, false },
	})
	_, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.Card(context.Background(), 5)
	if err == nil || !strings.Contains(err.Error(), "МБ") {
		t.Errorf("too large: %v, want the size named", err)
	}
}

// Бренды ниши (#623): ozon_category report=brands, имена из data.
func TestMCP_nicheBrands(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_category": func(a map[string]any) (string, bool) {
			if a["path"] != "Одежда/Футболки" || a["report"] != "brands" {
				return "wrong arguments", true
			}
			return `{"data":[{"name":"Бренд не указан","items":10},{"name":"Zara","items":3}],"summary":{}}`, false
		},
	})
	got, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.NicheBrands(context.Background(), "Одежда/Футболки")
	if err != nil || len(got) != 2 || got[1] != "Zara" {
		t.Fatalf("brands %v, %v", got, err)
	}
}

// Товары ниши и главное фото карточки (#608): ozon_category report=products
// и ozon_sku report=full (photo.count и photo.list[0].m — замер 14.11).
func TestMCP_productsAndPhoto(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_category": func(a map[string]any) (string, bool) {
			if a["report"] != "products" || a["limit"] != float64(50) {
				return "wrong arguments", true
			}
			return `{"total":2,"data":[{"id":11,"name":"Футболка","seller":"Продавец","revenue":1000,"final_price":900,"rating":4.8,"comments":120,"picscount":7,"hasvideo":1}]}`, false
		},
		"ozon_sku": func(map[string]any) (string, bool) {
			return `{"niche":{"name":"Одежда/Футболки"},"photo":{"count":6,"list":[{"f":"https://ir.ozone.ru/f.jpg","t":"https://ir.ozone.ru/t.jpg","m":"https://ir.ozone.ru/m.jpg"}]}}`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	ps, err := m.NicheProducts(context.Background(), "Одежда/Футболки", 50)
	if err != nil || len(ps) != 1 || ps[0].SKU != 11 || ps[0].Photos != 7 || !ps[0].Video || ps[0].Comments != 120 || ps[0].Price != 900 {
		t.Fatalf("products %+v, %v", ps, err)
	}
	c, err := m.Card(context.Background(), 11)
	if err != nil || c.Photo != "https://ir.ozone.ru/m.jpg" || c.Photos != 6 {
		t.Fatalf("card %+v, %v", c, err)
	}
}

// search_stats (замер 17.11): запросов и среднее место за период и за прошлый.
func TestMCP_searchStats(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_sku": func(a map[string]any) (string, bool) {
			if a["report"] != "search_stats" || a["d1"] != "2026-10-17" {
				return "wrong arguments", true
			}
			return `{"search_words_count":0,"search_words_count_previous":237,"search_position_avg":null,"search_position_avg_previous":17.4,` +
				`"search_visibility":0,"search_visibility_previous":1520,"oz_count":0,"oz_count_previous":12}`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	st, err := m.SearchStats(context.Background(), 1234567, time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	if err != nil || st.Words != 0 || st.WordsBefore != 237 || st.Avg != 0 || st.AvgBefore != 17 {
		t.Fatalf("%+v, %v", st, err)
	}
}
