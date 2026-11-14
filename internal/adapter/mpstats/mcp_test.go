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
			if a["report"] != "keywords" {
				return "wrong arguments", true
			}
			return `[{"query":"сиреневая футболка женская","count":49,"total":0,"avg_position":7,"positions":[7,null]}]`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	qs, ws, err := m.NicheQueries(context.Background(), "Одежда/Футболки")
	if err != nil || len(qs) != 1 || qs[0].Text != "футболка женская" || qs[0].Count != 111685 || qs[0].Items != 778 ||
		len(ws) != 1 || ws[0].Word != "женская" || len(ws[0].Forms) != 2 {
		t.Fatalf("niche queries = %+v %+v, %v", qs, ws, err)
	}
	own, err := m.CardQueries(context.Background(), 1234567)
	if err != nil || len(own) != 1 || own[0].Text != "сиреневая футболка женская" || own[0].Position != 7 || own[0].Count != 49 {
		t.Fatalf("card queries = %+v, %v", own, err)
	}
}

func TestMCP_refusals(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ozon_sku": func(a map[string]any) (string, bool) {
			if a["sku"] == float64(1) {
				return "", false // MPStats не знает товара: пустой ответ
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
	_, err := m.Card(ctx, 5)
	if err == nil || !strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "t-1") {
		t.Errorf("tool error: %v, want its text and no token", err)
	}
	if _, err := (mpstats.MCP{URL: "http://mcp.example/mcp", Token: "t-1"}).Card(ctx, 5); !errors.Is(err, mpstats.ErrInsecureURL) {
		t.Errorf("http: %v, want ErrInsecureURL (ключ — только по https)", err)
	}
}
