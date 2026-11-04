// Package kbserver is the client of a remote `kbengine serve`: окно и MCP
// спрашивают сервер-источник (VPS, Yandex Cloud, другой Mac) по HTTPS с
// токеном Bearer — тем же протоколом, каким говорят витрина и окно.
package kbserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Ошибки различимы, чтобы вызывающий подобрал слова: текст для человека живет
// у него (окно — serverMessage), здесь — нейтральные сигналы.
var (
	ErrUnauthorized = errors.New("kbserver: unauthorized")
	ErrUnreachable  = errors.New("kbserver: unreachable")
	ErrNotKB        = errors.New("kbserver: not a kb server")
	ErrServerError  = errors.New("kbserver: server error")
	// ErrRedirect — сервер перенаправил запрос. Клиент по редиректам не идет:
	// Go пересылает Authorization на тот же хост и при смене https на http.
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

// Search returns the server's entries for q, as the server encoded them and in
// its order: окно показывает их той же разметкой, что и записи своего каталога,
// и второй копии разбора записи здесь нет.
//
// ponytail: выдача приходит целиком (до 32 МБ на букву), хотя окну нужно 40
// записей и счет: склейке дублей нужны ключи всех записей, а /api/search
// предела не знает. Потолок — короткий запрос по большому удаленному каталогу.
// Путь: параметр limit и счет total в /api/search плюс ключи записей отдельно.
func (c Client) Search(ctx context.Context, q string) ([]json.RawMessage, error) {
	var entries []json.RawMessage
	if err := c.get(ctx, "/api/search", url.Values{"q": {q}}, maxSearchBody, &entries); err != nil {
		return nil, err
	}
	return entries, nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := &http.Client{}
	if c.HTTP != nil {
		copied := *c.HTTP // транспорт и таймаут вызывающего, но свое правило редиректов
		hc = &copied
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect }
	resp, err := hc.Do(req)
	if errors.Is(err, ErrRedirect) {
		return fmt.Errorf("%w: %s", ErrRedirect, path)
	}
	if err != nil {
		// Сеть, таймаут, DNS и недоверенный сертификат — для человека одно:
		// связи нет. Подробность остается в цепочке ошибки.
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
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

// statusErr maps a response status to the client's errors; nil for 200.
func statusErr(path string, code int) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ErrUnauthorized
	case code == http.StatusTooManyRequests || code >= http.StatusInternalServerError:
		// Настоящий kb-сервер с ошибкой или прокси при перезапуске: адрес верный.
		return fmt.Errorf("%w: %s answered %d", ErrServerError, path, code)
	default:
		return fmt.Errorf("%w: %s answered %d", ErrNotKB, path, code)
	}
}
