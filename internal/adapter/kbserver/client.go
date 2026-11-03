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
)

// maxBody — сколько читать из ответа: /api/engine и /api/stats весят килобайты,
// а чужой сервер по ошибочному адресу может прислать что угодно.
const maxBody = 1 << 20

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
	if err := c.get(ctx, "/api/engine", &engine); err != nil {
		return Info{}, err
	}
	var stats struct {
		Total *int `json:"total"`
	}
	if err := c.get(ctx, "/api/stats", &stats); err != nil {
		return Info{}, err
	}
	if engine.Version == "" || stats.Total == nil {
		return Info{}, ErrNotKB
	}
	return Info{Version: engine.Version, Entries: *stats.Total}, nil
}

func (c Client) get(ctx context.Context, path string, into any) error {
	target, err := url.JoinPath(c.URL, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(into); err != nil {
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
