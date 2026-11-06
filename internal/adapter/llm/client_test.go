package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/llm"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

const key = "not-a-real-key"

// Клиент говорит по OpenAI-совместимому /chat/completions — так отвечают и
// Vercel AI Gateway, и Ollama: ключ в Authorization, модель и инструменты в
// теле, вызовы инструментов разбираются из ответа.
func TestComplete_RequestAndToolCalls(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("path %s, auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":\"договор\"}"}}]}}]}`)
	}))
	defer srv.Close()

	c := llm.Client{BaseURL: srv.URL + "/v1", Key: key, Model: "anthropic/claude-sonnet-5.5", HTTP: srv.Client()}
	tools := []chat.Tool{{Name: "search", Description: "поиск", Parameters: json.RawMessage(`{"type":"object"}`)}}
	msgs := []chat.ModelMessage{
		{Role: chat.RoleSystem, Content: "sys"},
		{Role: chat.RoleUser, Content: "найди договор"},
		{Role: chat.RoleAssistant, ToolCalls: []chat.ToolCall{{ID: "c0", Name: "read", Arguments: `{"ref":"kb:1"}`}}},
		{Role: chat.RoleTool, ToolCallID: "c0", Content: "текст"},
	}
	got, err := c.Complete(context.Background(), msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "search" || got.ToolCalls[0].Arguments != `{"q":"договор"}` || got.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %+v", got.ToolCalls)
	}
	if body["model"] != "anthropic/claude-sonnet-5.5" {
		t.Errorf("model = %v", body["model"])
	}
	ms := body["messages"].([]any)
	if len(ms) != 4 || ms[3].(map[string]any)["tool_call_id"] != "c0" || ms[2].(map[string]any)["tool_calls"] == nil {
		t.Errorf("messages = %v", ms)
	}
	tl := body["tools"].([]any)[0].(map[string]any)
	if tl["type"] != "function" || tl["function"].(map[string]any)["name"] != "search" {
		t.Errorf("tools = %v", tl)
	}
}

// Без инструментов поле tools не уходит: часть локальных моделей отвечает на
// пустой список ошибкой.
func TestComplete_TextAnswerNoTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), `"tools"`) {
			t.Errorf("tools ушли без инструментов: %s", b)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("без ключа Authorization не нужен (Ollama): %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Ответ [1]"}}]}`)
	}))
	defer srv.Close()
	got, err := llm.Client{BaseURL: srv.URL + "/v1", Model: "qwen3", HTTP: srv.Client()}.
		Complete(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil)
	if err != nil || got.Content != "Ответ [1]" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// Отказы — свои ошибки, ключ не попадает в текст ошибки ни при каком ответе.
func TestComplete_Errors(t *testing.T) {
	tests := []struct {
		code int
		body string
		want error
	}{
		{http.StatusUnauthorized, `{"error":{"message":"bad key ` + key + `"}}`, llm.ErrUnauthorized},
		{http.StatusForbidden, `{"error":{"message":"Free tier users do not have access"}}`, llm.ErrUnauthorized},
		{http.StatusPaymentRequired, `{"error":{"message":"no credits"}}`, llm.ErrNoCredit},
		{http.StatusNotFound, `{"error":{"message":"model not found"}}`, llm.ErrModelNotFound},
		{http.StatusTooManyRequests, `{}`, llm.ErrServer},
		{http.StatusInternalServerError, `oops`, llm.ErrServer},
		{http.StatusOK, `not json`, llm.ErrBadReply},
		{http.StatusOK, `{"choices":[]}`, llm.ErrBadReply},
	}
	for _, tc := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = io.WriteString(w, tc.body)
		}))
		_, err := llm.Client{BaseURL: srv.URL, Key: key, Model: "m", HTTP: srv.Client()}.
			Complete(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil)
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("%d %s: err = %v, want %v", tc.code, tc.body, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), key) {
			t.Errorf("%d: ключ в тексте ошибки: %v", tc.code, err)
		}
	}
}

// Ключ уходит только на https или на этот Мак (Ollama): на чужой http он
// прошел бы по сети открытым.
func TestComplete_KeyOnlyOverHTTPSOrLoopback(t *testing.T) {
	_, err := llm.Client{BaseURL: "http://192.0.2.10:11434/v1", Key: key, Model: "m"}.
		Complete(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil)
	if !errors.Is(err, llm.ErrInsecureURL) {
		t.Fatalf("err = %v, want ErrInsecureURL", err)
	}
}

// Поток (stream: true): куски текста приходят строками SSE, show видит
// растущий текст; вызов инструмента собирается из кусков по index.
func TestStream_TextAndToolCalls(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"Сейчас"}}]}`,
			`: keep-alive`,
			`data: {"choices":[{"delta":{"content":" поищу"}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"срок\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, line+"\n\n")
		}
	}))
	defer srv.Close()
	var seen []string
	got, err := llm.Client{BaseURL: srv.URL + "/v1", Key: key, Model: "m", HTTP: srv.Client()}.
		Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}},
			[]chat.Tool{{Name: "search", Parameters: json.RawMessage(`{"type":"object"}`)}}, func(s string) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v", body["stream"])
	}
	if strings.Join(seen, "|") != "Сейчас|Сейчас поищу" || got.Content != "Сейчас поищу" {
		t.Fatalf("seen %q, content %q", seen, got.Content)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" || got.ToolCalls[0].Name != "search" || got.ToolCalls[0].Arguments != `{"q":"срок"}` {
		t.Fatalf("tool calls = %+v", got.ToolCalls)
	}
}

// Отказы потока — те же ошибки, что у Complete, и без ключа в тексте;
// поток без единого куска и ошибка внутри потока — тоже отказ.
func TestStream_Errors(t *testing.T) {
	tests := []struct {
		code int
		body string
		want error
	}{
		{http.StatusUnauthorized, `{"error":{"message":"bad key ` + key + `"}}`, llm.ErrUnauthorized},
		{http.StatusPaymentRequired, `{}`, llm.ErrNoCredit},
		{http.StatusOK, "data: [DONE]\n\n", llm.ErrBadReply},
		{http.StatusOK, "", llm.ErrBadReply},
		{http.StatusOK, `data: {"error":{"message":"overloaded ` + key + `"}}` + "\n\n", llm.ErrServer},
	}
	for _, tc := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = io.WriteString(w, tc.body)
		}))
		_, err := llm.Client{BaseURL: srv.URL, Key: key, Model: "m", HTTP: srv.Client()}.
			Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil, func(string) {})
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("%d %q: err = %v, want %v", tc.code, tc.body, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), key) {
			t.Errorf("%d: ключ в тексте ошибки: %v", tc.code, err)
		}
	}
	_, err := llm.Client{BaseURL: "http://192.0.2.10:11434/v1", Key: key, Model: "m"}.
		Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil, func(string) {})
	if !errors.Is(err, llm.ErrInsecureURL) {
		t.Fatalf("err = %v, want ErrInsecureURL", err)
	}
}

// Поток, оборвавшийся до [DONE] и без finish_reason, — отказ: урезанный ответ
// не сохраняется как целый (ревью #463).
func TestStream_TruncatedIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Срок —\"}}]}\n\n")
	}))
	defer srv.Close()
	_, err := llm.Client{BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}.
		Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil, func(string) {})
	if !errors.Is(err, llm.ErrServer) {
		t.Fatalf("err = %v, want ErrServer", err)
	}
	// finish_reason без [DONE] — поток кончился честно.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Срок 30\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer srv2.Close()
	got, err := llm.Client{BaseURL: srv2.URL, Model: "m", HTTP: srv2.Client()}.
		Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil, func(string) {})
	if err != nil || got.Content != "Срок 30" {
		t.Fatalf("finish_reason: %+v, %v", got, err)
	}
}

// Сервер, что на stream: true ответил обычным JSON, — ответ целиком, а не отказ.
func TestStream_JSONAnswerFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Ответ целиком"}}]}`)
	}))
	defer srv.Close()
	var seen []string
	got, err := llm.Client{BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}.
		Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil, func(s string) { seen = append(seen, s) })
	if err != nil || got.Content != "Ответ целиком" || len(seen) != 1 {
		t.Fatalf("got %+v, %v, seen %q", got, err, seen)
	}
}

// Параллельные вызовы без index (он читается как 0), но с разными id — два
// вызова, а не склейка аргументов.
func TestStream_ParallelCallsWithoutIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"id":"a","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"id":"b","function":{"name":"search","arguments":"{\"q\":\"y\"}"}}]}}]}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, line+"\n\n")
		}
	}))
	defer srv.Close()
	got, err := llm.Client{BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}.
		Stream(context.Background(), []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}, nil, func(string) {})
	if err != nil || len(got.ToolCalls) != 2 || got.ToolCalls[0].Arguments != `{"q":"x"}` || got.ToolCalls[1].ID != "b" {
		t.Fatalf("got %+v, %v", got.ToolCalls, err)
	}
}
