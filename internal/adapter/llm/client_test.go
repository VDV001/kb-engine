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
