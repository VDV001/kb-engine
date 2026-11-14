package mpstats

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// MCPURL — MCP MPStats (#607): ключевые слова ниши и карточка товара Ozon есть
// в нем с описанием инструментов (tools/list, замер 14.11), а REST-адреса
// этих отчетов видны только в документации за входом. Ключ — в адресе
// (?token=), так его выдает MPStats.
const MCPURL = "https://mcp.mpstats.io/mcp"

// ErrNotFound — MPStats ответил пусто: товара или ниши у него нет.
var ErrNotFound = errors.New("MPStats не знает этого товара")

// maxMCPBody — ключевые слова ниши — 5000 запросов и 2500 слов, 4,5 МБ (замер 14.11).
const maxMCPBody = 16 << 20

// MCP calls MPStats tools over MCP with a token.
type MCP struct {
	URL   string
	Token string
	HTTP  *http.Client // nil — свой, с пределом в полторы минуты
}

// Card — карточка товара Ozon в MPStats: путь ниши, бренд и продавец.
type Card struct {
	Name, Niche, Brand, Seller string
}

// Card reads sku's card (ozon_sku report=full).
func (m MCP) Card(ctx context.Context, sku int64) (Card, error) {
	var raw struct {
		Name  string `json:"name"`
		Brand string `json:"brand"`
		Niche struct {
			Name string `json:"name"`
		} `json:"niche"`
		Seller struct {
			Name string `json:"name"`
		} `json:"seller"`
	}
	if err := m.call(ctx, "ozon_sku", map[string]any{"sku": sku, "report": "full"}, &raw); err != nil {
		return Card{}, err
	}
	return Card{Name: raw.Name, Niche: raw.Niche.Name, Brand: raw.Brand, Seller: raw.Seller.Name}, nil
}

// CardQueries reads the queries sku is found by in 30 days to yesterday (ozon_sku report=keywords).
func (m MCP) CardQueries(ctx context.Context, sku int64) ([]markets.OwnQuery, error) {
	var rows []struct {
		Query    string  `json:"query"`
		Count    int     `json:"count"`
		Position float64 `json:"avg_position"`
	}
	if err := m.call(ctx, "ozon_sku", map[string]any{"sku": sku, "report": "keywords"}, &rows); err != nil {
		return nil, err
	}
	out := make([]markets.OwnQuery, 0, len(rows))
	for _, r := range rows {
		out = append(out, markets.OwnQuery{Text: r.Query, Count: r.Count, Position: int(r.Position)})
	}
	return out, nil
}

// NicheQueries reads niche path's search queries and word forms in 30 days to
// yesterday (ozon_category report=keywords).
func (m MCP) NicheQueries(ctx context.Context, path string) ([]markets.NicheQuery, []markets.WordForms, error) {
	var raw struct {
		Queries []struct {
			Word  string `json:"word"`
			Count int    `json:"count"`
			Items int    `json:"items_count"`
		} `json:"queries"`
		Words []struct {
			Word  string   `json:"word"`
			Forms []string `json:"words"`
		} `json:"words"`
	}
	if err := m.call(ctx, "ozon_category", map[string]any{"path": path, "report": "keywords"}, &raw); err != nil {
		return nil, nil, err
	}
	qs := make([]markets.NicheQuery, 0, len(raw.Queries))
	for _, q := range raw.Queries {
		qs = append(qs, markets.NicheQuery{Text: q.Word, Count: q.Count, Items: q.Items})
	}
	ws := make([]markets.WordForms, 0, len(raw.Words))
	for _, w := range raw.Words {
		ws = append(ws, markets.WordForms{Word: w.Word, Forms: w.Forms})
	}
	return qs, ws, nil
}

// call opens a session and calls tool with args; the tool's text JSON goes into out.
// ponytail: сессия на каждый вызов — два лишних коротких запроса (initialize
// квоту не тратит); потолок — частые вызовы подряд, апгрейд — держать сессию.
func (m MCP) call(ctx context.Context, tool string, args map[string]any, out any) error {
	s, err := m.open(ctx)
	if err != nil {
		return err
	}
	body, _, err := s.post(map[string]any{"id": 2, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		return err
	}
	var res toolAnswer
	if err := rpcResult(bytes.NewReader(body), 2, &res); err != nil {
		return err
	}
	return res.decode(out)
}

// mcpSession — открытая сессия MCP: адрес с ключом и Mcp-Session-Id.
type mcpSession struct {
	ctx    context.Context
	client *http.Client
	target string
	sid    string
}

// open checks the address, then initialize → Mcp-Session-Id → notifications/initialized.
func (m MCP) open(ctx context.Context) (*mcpSession, error) {
	if !safeForKey(m.URL) {
		return nil, ErrInsecureURL
	}
	u, err := url.Parse(m.URL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("token", m.Token)
	u.RawQuery = q.Encode()
	client := &http.Client{Timeout: 90 * time.Second} // ключевые слова ниши — 8 с, запас на загруженный MPStats
	if m.HTTP != nil {
		cp := *m.HTTP
		client = &cp
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect } // ключ — только на URL
	s := &mcpSession{ctx: ctx, client: client, target: u.String()}
	body, h, err := s.post(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "kb-desktop", "version": "1"}}})
	if err != nil {
		return nil, err
	}
	if err := rpcResult(bytes.NewReader(body), 1, &json.RawMessage{}); err != nil {
		return nil, err
	}
	s.sid = h.Get("Mcp-Session-Id")
	if _, _, err := s.post(map[string]any{"method": "notifications/initialized"}); err != nil {
		return nil, err
	}
	return s, nil
}

// post sends one JSON-RPC message; ответ 2xx — тело и заголовки, иначе причина.
func (s *mcpSession) post(msg map[string]any) ([]byte, http.Header, error) {
	msg["jsonrpc"] = "2.0"
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.target, bytes.NewReader(b))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.sid != "" {
		req.Header.Set("Mcp-Session-Id", s.sid)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, transportErr(s.ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 { // уведомление — 202
		return nil, nil, statusErr(resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPBody))
	if err != nil {
		return nil, nil, ErrDropped
	}
	return data, resp.Header, nil
}

// toolAnswer — результат tools/call: текст инструмента и признак ошибки.
type toolAnswer struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// decode puts the tool's JSON text into out; пусто — ErrNotFound, ошибка инструмента — ее текст.
func (a toolAnswer) decode(out any) error {
	if len(a.Content) == 0 || strings.TrimSpace(a.Content[0].Text) == "" {
		return ErrNotFound
	}
	text := a.Content[0].Text
	if a.IsError {
		return fmt.Errorf("MPStats: %s", clip(text, 200))
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("MPStats ответил не так, как ожидалось: %s", clip(text, 120))
	}
	return nil
}

// rpcResult finds the JSON-RPC answer id in a JSON or SSE body and decodes its result.
func rpcResult(body io.Reader, id int, out any) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return ErrDropped
	}
	msgs := [][]byte{data}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) { // SSE: строки «data: {…}»
		msgs = nil
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(nil, maxMCPBody)
		for sc.Scan() {
			if line, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:")); ok {
				msgs = append(msgs, bytes.Clone(bytes.TrimSpace(line)))
			}
		}
	}
	for _, b := range msgs {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &msg) != nil || msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("MPStats: %s", clip(msg.Error.Message, 200))
		}
		return json.Unmarshal(msg.Result, out)
	}
	return errors.New("MPStats не прислал ответа")
}

// clip shortens s to n runes for an error message.
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
