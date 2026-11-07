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
	// Счет токенов в потоке OpenAI-совместимые серверы шлют только по просьбе.
	StreamOptions *wireStreamOptions `json:"stream_options,omitempty"`
}

type wireReply struct {
	Choices []struct {
		Message wireMessage `json:"message"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// wireUsage is the provider's token count.
type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (u *wireUsage) usage() chat.Usage {
	if u == nil {
		return chat.Usage{}
	}
	return chat.Usage{In: u.PromptTokens, Out: u.CompletionTokens, Counted: true}
}

// Complete implements chat.Model.
func (c Client) Complete(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool) (chat.ModelReply, error) {
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
	// Ключ уходит только по https или на этот Мак — в одном месте, где ставится заголовок.
	if c.Key != "" && !safeForKey(c.BaseURL) {
		return nil, ErrInsecureURL
	}
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
		Delta        wireDelta `json:"delta"`
		FinishReason *string   `json:"finish_reason"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
	Usage *wireUsage      `json:"usage"`
}

// maxToolCalls caps the tool calls of one streamed reply: index приходит от
// модели, и без потолка битый поток раздул бы список.
const maxToolCalls = 32

// Stream implements chat.Streamer: тот же запрос со stream: true, ответ —
// строки SSE «data: {…}» до «data: [DONE]». show получает текст, накопленный
// к каждому куску; вызовы инструментов собираются из кусков по index.
func (c Client) Stream(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool, show func(string)) (chat.ModelReply, error) {
	r := request(c.Model, msgs, tools)
	r.Stream = true
	r.StreamOptions = &wireStreamOptions{IncludeUsage: true}
	res, err := c.send(ctx, r)
	if errors.Is(err, errBadRequest) {
		// Строгий сервер не знает stream_options: поток без счета лучше отказа.
		r.StreamOptions = nil
		res, err = c.send(ctx, r)
	}
	if err != nil {
		return chat.ModelReply{}, err
	}
	defer func() { _ = res.Body.Close() }()
	// Сервер, что не умеет поток, отвечает обычным JSON: это ответ целиком.
	if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
		if err != nil {
			return chat.ModelReply{}, fmt.Errorf("%w: %w", ErrServer, err)
		}
		reply, err := parse(raw)
		if err == nil && reply.Content != "" {
			show(reply.Content)
		}
		return reply, err
	}
	return readStream(io.LimitReader(res.Body, maxReply), show)
}

// readStream reads SSE events until [DONE] or the end of the body.
func readStream(body io.Reader, show func(string)) (chat.ModelReply, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxReply)
	var (
		acc  streamed
		got  bool
		done bool // [DONE] или finish_reason: поток кончился сам, а не оборвался
	)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue // пустая строка между событиями, «: keep-alive»
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			break
		}
		ch, err := decodeChunk(data)
		if err != nil {
			return chat.ModelReply{Usage: acc.use}, err // счет провайдер уже посчитал
		}
		got = true
		fin, err := acc.take(ch, show)
		if err != nil {
			return chat.ModelReply{Usage: acc.use}, err
		}
		done = done || fin
	}
	if err := sc.Err(); err != nil {
		return chat.ModelReply{Usage: acc.use}, fmt.Errorf("%w: %w", ErrServer, err)
	}
	if !got {
		return chat.ModelReply{}, ErrBadReply
	}
	if !done {
		return chat.ModelReply{Usage: acc.use}, fmt.Errorf("%w: поток оборвался до конца ответа", ErrServer)
	}
	return chat.ModelReply{Content: acc.text.String(), ToolCalls: acc.calls, Usage: acc.use}, nil
}

// take adds one event; true — поток кончился сам (finish_reason).
func (a *streamed) take(ch wireChunk, show func(string)) (bool, error) {
	if ch.Usage != nil {
		a.use = ch.Usage.usage()
	}
	if len(ch.Choices) == 0 {
		return false, nil // кусок со счетом токенов
	}
	if err := a.add(ch.Choices[0].Delta, show); err != nil {
		return false, err
	}
	fr := ch.Choices[0].FinishReason
	return fr != nil && *fr != "", nil
}

// decodeChunk reads one event; ошибка внутри потока — отказ без ее текста (см. send).
func decodeChunk(data string) (wireChunk, error) {
	var ch wireChunk
	if json.Unmarshal([]byte(data), &ch) != nil {
		return ch, ErrBadReply
	}
	if len(ch.Error) > 0 && string(ch.Error) != "null" {
		return ch, fmt.Errorf("%w: error in stream", ErrServer)
	}
	return ch, nil
}

// streamed is the reply put together from the pieces so far.
type streamed struct {
	text  strings.Builder
	calls []chat.ToolCall
	slot  map[int]int // index потока → место в calls
	use   chat.Usage  // счет токенов: в последнем событии без choices
}

// slotFor returns where the pieces of call index go: новый вызов — новый index
// или тот же index с другим id (провайдер без index, он читается как 0,
// так не склеивает параллельные вызовы).
func (a *streamed) slotFor(index int, id string) (int, error) {
	if a.slot == nil {
		a.slot = map[int]int{}
	}
	pos, ok := a.slot[index]
	if ok && (id == "" || a.calls[pos].ID == "" || a.calls[pos].ID == id) {
		return pos, nil
	}
	if len(a.calls) >= maxToolCalls {
		return 0, ErrBadReply
	}
	a.calls = append(a.calls, chat.ToolCall{})
	a.slot[index] = len(a.calls) - 1
	return len(a.calls) - 1, nil
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
		pos, err := a.slotFor(tc.Index, tc.ID)
		if err != nil {
			return err
		}
		cl := &a.calls[pos]
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
	out := chat.ModelReply{Usage: w.Usage.usage()}
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

// errBadRequest is a 400: Stream повторяет запрос без stream_options.
var errBadRequest = errors.New("status 400")

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
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %w", ErrServer, errBadRequest)
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
