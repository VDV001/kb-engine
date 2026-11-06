// Package kbserver is the client of a remote `kbengine serve`: окно и MCP
// спрашивают сервер-источник (VPS, Yandex Cloud, другой Mac) по HTTPS с
// токеном в заголовке X-KB-Token.
package kbserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
)

// Ошибки различимы, чтобы вызывающий подобрал слова: текст для человека живет
// у него (окно — serverMessage), здесь — нейтральные сигналы.
var (
	ErrUnauthorized = errors.New("kbserver: unauthorized")
	// ErrForbidden — 403: отказ до приложения (облако, прокси), а не токен.
	// Обертка ErrUnauthorized: кто различает только «не пустили», различает.
	ErrForbidden   = fmt.Errorf("%w: forbidden before the app", ErrUnauthorized)
	ErrUnreachable = errors.New("kbserver: unreachable")
	ErrNotKB       = errors.New("kbserver: not a kb server")
	ErrServerError = errors.New("kbserver: server error")
	// ErrRedirect — сервер перенаправил запрос. Клиент по редиректам не идет:
	// Go пересылает свой заголовок X-KB-Token на любой хост, даже чужой.
	ErrRedirect = errors.New("kbserver: redirect refused")
	// ErrTooLarge — ответ больше предела чтения: адрес верный и сервер здоров,
	// просто выдача велика (короткий запрос по большому каталогу).
	ErrTooLarge = errors.New("kbserver: response too large")
)

// maxBody — сколько читать из ответа: /api/engine и /api/stats весят килобайты,
// а чужой сервер по ошибочному адресу может прислать что угодно. Выдача поиска
// по каталогу в тысячи записей — мегабайты, у нее предел свой.
const (
	maxBody       = 1 << 20
	maxSearchBody = 32 << 20
)

// TokenHeader is the header the app token travels in; тот же, что читает
// сервер (httpapi.TokenHeader, тест сверяет).
const TokenHeader = "X-KB-Token"

// Client talks to one kb server. HTTP — nil значит http.DefaultClient; тесты
// и окно подставляют свой (доверие к сертификату, таймауты).
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// Info is what the sources screen shows for a server: «kbengine 0.29.0 · 2 431».
type Info struct {
	Version string `json:"version"`
	Entries int    `json:"entries"`
}

// Info asks the server for its version and the number of entries.
func (c Client) Info(ctx context.Context) (Info, error) {
	var engine struct {
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/api/engine", nil, maxBody, &engine); err != nil {
		return Info{}, err
	}
	var stats struct {
		Total *int `json:"total"`
	}
	if err := c.get(ctx, "/api/stats", nil, maxBody, &stats); err != nil {
		return Info{}, err
	}
	if engine.Version == "" || stats.Total == nil {
		return Info{}, ErrNotKB
	}
	return Info{Version: engine.Version, Entries: *stats.Total}, nil
}

// Result is what a server found for a query: the entries as the server
// encoded them, in its order; how many it found; and the merge keys of the
// entries it did not send (сервер с limit). Сервер без limit (0.30.0) присылает
// все найденное, и Keys пуст.
type Result struct {
	Entries []json.RawMessage
	Total   int
	Keys    []string
}

// Search asks the server for q. limit > 0 asks for only the first limit
// entries plus the count and the merge keys of the rest: склейке дублей нужны
// ключи всех записей, а сами записи — только показанные. Сервер, что limit не
// знает, отвечает массивом всего найденного, и клиент понимает оба ответа.
func (c Client) Search(ctx context.Context, q string, limit int) (Result, error) {
	params := url.Values{"q": {q}}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var body searchBody
	if err := c.get(ctx, "/api/search", params, maxSearchBody, &body); err != nil {
		return Result{}, err
	}
	r := body.Result
	// Счет сходится с присланным: иначе «найдено» в окне врало бы. Ключ — не
	// простыня: это адрес статьи или номер с заголовком.
	if r.Total != len(r.Entries)+len(r.Keys) {
		return Result{}, fmt.Errorf("%w: /api/search: total %d при %d записях и %d ключах", ErrNotKB, r.Total, len(r.Entries), len(r.Keys))
	}
	for _, k := range r.Keys {
		if len(k) > maxKeyLen {
			return Result{}, fmt.Errorf("%w: /api/search: ключ длиннее %d байт", ErrNotKB, maxKeyLen)
		}
	}
	return r, nil
}

// maxKeyLen — предел длины ключа склейки: адрес статьи или номер с заголовком.
const maxKeyLen = 2048

// searchBody decodes either answer of /api/search in one pass: массив сервера
// без limit (0.30.0), объект сервера с limit или null — пустая выдача.
type searchBody struct{ Result }

// UnmarshalJSON switches on the first byte of the answer.
func (b *searchBody) UnmarshalJSON(raw []byte) error {
	t := bytes.TrimLeft(raw, " \t\r\n")
	switch {
	case bytes.HasPrefix(t, []byte("null")):
		b.Result = Result{}
		return nil
	case len(t) > 0 && t[0] == '[':
		var entries []json.RawMessage
		if err := json.Unmarshal(t, &entries); err != nil {
			return err
		}
		b.Result = Result{Entries: entries, Total: len(entries)}
		return nil
	}
	var obj struct {
		Entries *[]json.RawMessage `json:"entries"`
		Total   int                `json:"total"`
		Keys    []string           `json:"keys"`
	}
	if err := json.Unmarshal(t, &obj); err != nil {
		return err
	}
	if obj.Entries == nil {
		return errors.New("нет entries")
	}
	b.Result = Result{Entries: *obj.Entries, Total: obj.Total, Keys: obj.Keys}
	return nil
}

// get asks path with the query params. Параметры идут отдельно от пути:
// JoinPath экранировал бы «?», а склейка строкой оборвала бы запрос на «&».
func (c Client) get(ctx context.Context, path string, params url.Values, limit int64, into any) error {
	target, err := url.JoinPath(c.URL, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	resp, err := c.send(ctx, http.MethodGet, target, path, nil, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusErr(path, resp.StatusCode); err != nil {
		return err
	}
	// Байт сверх предела читается, чтобы отличить «слишком велик» от «не JSON»:
	// обрезанный ответ иначе выглядел бы чужим сервером.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnreachable, path, err)
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("%w: %s больше %d байт", ErrTooLarge, path, limit)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotKB, path, err)
	}
	return nil
}

// send makes one request with the token and without redirects: Go пересылает
// заголовок X-KB-Token на любой хост редиректа, даже чужой.
func (c Client) send(ctx context.Context, method, target, path string, body []byte, header http.Header) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	maps.Copy(req.Header, header)
	// Токен в X-KB-Token, а не в Authorization: Serverless Container
	// вырезает Authorization и проверяет Bearer как свой IAM-токен. Сервер
	// понимает оба заголовка.
	if c.Token != "" {
		req.Header.Set(TokenHeader, c.Token)
	}
	hc := &http.Client{}
	if c.HTTP != nil {
		copied := *c.HTTP // транспорт и таймаут вызывающего, но свое правило редиректов
		hc = &copied
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect }
	resp, err := hc.Do(req)
	if errors.Is(err, ErrRedirect) {
		return nil, fmt.Errorf("%w: %s", ErrRedirect, path)
	}
	if err != nil {
		// Сеть, таймаут, DNS и недоверенный сертификат — для человека одно:
		// связи нет. Подробность остается в цепочке ошибки.
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return resp, nil
}

// statusErr maps a response status to the client's errors; nil for 200.
func statusErr(path string, code int) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized:
		return ErrUnauthorized
	case code == http.StatusForbidden:
		return ErrForbidden
	case code == http.StatusTooManyRequests || code >= http.StatusInternalServerError:
		// Настоящий kb-сервер с ошибкой или прокси при перезапуске: адрес верный.
		return fmt.Errorf("%w: %s answered %d", ErrServerError, path, code)
	default:
		return fmt.Errorf("%w: %s answered %d", ErrNotKB, path, code)
	}
}

// Describe turns a client error into words for a person: окно (строка
// источников, «Проверить», «Проблемы») и MCP говорят об одной беде одинаково.
// Форма «что не так — что сделать»: панель «Проблемы» делит фразу по « — ».
func Describe(err error) string {
	switch {
	case errors.Is(err, ErrForbidden):
		return "адрес ответил 403: вход закрыт до kb-сервера (облако или прокси) — проверьте адрес"
	case errors.Is(err, ErrUnauthorized):
		return "сервер ответил 401: токен неверный, или сервер старой версии и не знает X-KB-Token — введите токен заново в «Источниках» или обновите сервер"
	case errors.Is(err, ErrNotKB):
		return "по адресу отвечает не kb-сервер — проверьте адрес"
	case errors.Is(err, ErrRedirect):
		return "сервер перенаправил запрос, а kb не идет по редиректам с токеном — укажите конечный адрес (https://…)"
	case errors.Is(err, ErrTooLarge):
		return "выдача сервера больше 32 МБ — уточните запрос"
	case errors.Is(err, ErrServerError):
		return "kb-сервер ответил ошибкой: адрес верный, сервер не в порядке — попробуйте позже или посмотрите журнал сервера"
	default:
		return "нет связи с сервером — проверьте адрес и VPN"
	}
}
