// Package llm is a client of the OpenAI-compatible /chat/completions API —
// так говорят и Vercel AI Gateway, и Ollama, поэтому окну DVK хватает одного
// клиента на ключ владельца и на локальную модель. Без SDK: запрос и ответ —
// несколько полей JSON.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// Отказы модели: окно называет их словами человека.
var (
	ErrUnauthorized  = errors.New("model refused the key")
	ErrNoCredit      = errors.New("no credit left for the model")
	ErrModelNotFound = errors.New("model not found")
	ErrServer        = errors.New("model server error")
	ErrBadReply      = errors.New("model reply is not understood")
	ErrInsecureURL   = errors.New("key only goes over https or to this Mac")
)

// maxReply caps the reply body: ответ модели — текст, а не мегабайты.
const maxReply = 4 << 20

// Client asks one model at BaseURL (…/v1). Key пусто — без Authorization
// (Ollama); ключ не попадает ни в ошибки, ни в журнал.
type Client struct {
	BaseURL, Key, Model string
	HTTP                *http.Client // nil — http.DefaultClient; срок задает ctx
}

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream,omitempty"`
}

type wireReply struct {
	Choices []struct {
		Message wireMessage `json:"message"`
	} `json:"choices"`
}

// Complete implements chat.Model.
func (c Client) Complete(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool) (chat.ModelReply, error) {
	if c.Key != "" && !safeForKey(c.BaseURL) {
		return chat.ModelReply{}, ErrInsecureURL
	}
	raw, err := c.post(ctx, request(c.Model, msgs, tools))
	if err != nil {
		return chat.ModelReply{}, err
	}
	return parse(raw)
}

// post sends the request and returns the body of a 200 answer.
func (c Client) post(ctx context.Context, r wireRequest) ([]byte, error) {
	res, err := c.send(ctx, r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServer, err)
	}
	return raw, nil
}

// send posts the request; ответ не 200 — ошибка, тело закрыто.
func (c Client) send(ctx context.Context, r wireRequest) (*http.Response, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServer, err)
	}
	// Текст ответа с ошибкой не берется: провайдер мог повторить в нем ключ.
	if err := statusErr(res.StatusCode); err != nil {
		_ = res.Body.Close()
		return nil, err
	}
	return res, nil
}

// wireDelta is the piece of the reply in one streamed event.
type wireDelta struct {
	Content   *string `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// wireChunk is one event of a streamed reply.
type wireChunk struct {
	Choices []struct {
		Delta wireDelta `json:"delta"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

// maxToolCalls caps the tool calls of one streamed reply: index приходит от
// модели, и без потолка битый поток раздул бы список.
const maxToolCalls = 32

// Stream implements chat.Streamer: тот же запрос со stream: true, ответ —
// строки SSE «data: {…}» до «data: [DONE]». show получает текст, накопленный
// к каждому куску; вызовы инструментов собираются из кусков по index.
func (c Client) Stream(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool, show func(string)) (chat.ModelReply, error) {
	if c.Key != "" && !safeForKey(c.BaseURL) {
		return chat.ModelReply{}, ErrInsecureURL
	}
	r := request(c.Model, msgs, tools)
	r.Stream = true
	res, err := c.send(ctx, r)
	if err != nil {
		return chat.ModelReply{}, err
	}
	defer func() { _ = res.Body.Close() }()
	return readStream(io.LimitReader(res.Body, maxReply), show)
}

// readStream reads SSE events until [DONE] or the end of the body.
func readStream(body io.Reader, show func(string)) (chat.ModelReply, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxReply)
	var (
		acc streamed
		got bool
	)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue // пустая строка между событиями, «: keep-alive»
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch wireChunk
		if json.Unmarshal([]byte(data), &ch) != nil {
			return chat.ModelReply{}, ErrBadReply
		}
		if len(ch.Error) > 0 && string(ch.Error) != "null" {
			return chat.ModelReply{}, fmt.Errorf("%w: error in stream", ErrServer) // текст ошибки не берется, см. send
		}
		got = true
		if len(ch.Choices) == 0 {
			continue // кусок со счетом токенов
		}
		if err := acc.add(ch.Choices[0].Delta, show); err != nil {
			return chat.ModelReply{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return chat.ModelReply{}, fmt.Errorf("%w: %w", ErrServer, err)
	}
	if !got {
		return chat.ModelReply{}, ErrBadReply
	}
	return chat.ModelReply{Content: acc.text.String(), ToolCalls: acc.calls}, nil
}

// streamed is the reply put together from the pieces so far.
type streamed struct {
	text  strings.Builder
	calls []chat.ToolCall
}

func (a *streamed) add(d wireDelta, show func(string)) error {
	if d.Content != nil && *d.Content != "" {
		a.text.WriteString(*d.Content)
		show(a.text.String())
	}
	for _, tc := range d.ToolCalls {
		if tc.Index < 0 || tc.Index >= maxToolCalls {
			return ErrBadReply
		}
		for len(a.calls) <= tc.Index {
			a.calls = append(a.calls, chat.ToolCall{})
		}
		cl := &a.calls[tc.Index]
		if tc.ID != "" {
			cl.ID = tc.ID
		}
		if tc.Function.Name != "" {
			cl.Name = tc.Function.Name
		}
		cl.Arguments += tc.Function.Arguments
	}
	return nil
}

// parse reads the first choice of a reply.
func parse(raw []byte) (chat.ModelReply, error) {
	var w wireReply
	if err := json.Unmarshal(raw, &w); err != nil || len(w.Choices) == 0 {
		return chat.ModelReply{}, ErrBadReply
	}
	m := w.Choices[0].Message
	out := chat.ModelReply{}
	if m.Content != nil {
		out.Content = *m.Content
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, chat.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return out, nil
}

func request(model string, msgs []chat.ModelMessage, tools []chat.Tool) wireRequest {
	r := wireRequest{Model: model, Messages: make([]wireMessage, 0, len(msgs))}
	for _, m := range msgs {
		w := wireMessage{Role: string(m.Role), ToolCallID: m.ToolCallID}
		if m.Content != "" || len(m.ToolCalls) == 0 {
			content := m.Content
			w.Content = &content
		}
		for _, tc := range m.ToolCalls {
			var wt wireToolCall
			wt.ID, wt.Type = tc.ID, "function"
			wt.Function.Name, wt.Function.Arguments = tc.Name, tc.Arguments
			w.ToolCalls = append(w.ToolCalls, wt)
		}
		r.Messages = append(r.Messages, w)
	}
	for _, t := range tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name, wt.Function.Description, wt.Function.Parameters = t.Name, t.Description, t.Parameters
		r.Tools = append(r.Tools, wt)
	}
	return r
}

func statusErr(code int) error {
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusPaymentRequired:
		return ErrNoCredit
	case http.StatusNotFound:
		return ErrModelNotFound
	default:
		return fmt.Errorf("%w: status %d", ErrServer, code)
	}
}

// safeForKey reports whether a key may go to base: https или этот Мак.
func safeForKey(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
