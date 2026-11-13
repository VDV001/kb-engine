// Package mpstats reads niche analytics from the MPStats API (#580): продажи
// категории Ozon по дням — для «Прогноза» вместо выгрузки файлом.
package mpstats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// DefaultURL — API MPStats; ключ — в заголовке X-Mpstats-TOKEN (живой ответ 13.11).
const DefaultURL = "https://mpstats.io"

// Ошибки API.
var (
	ErrUnauthorized = errors.New("ключ MPStats не принят")
	ErrInsecureURL  = errors.New("ключ MPStats уходит только по https")
	ErrQuota        = errors.New("лимит запросов тарифа MPStats исчерпан")
	ErrRedirect     = errors.New("перенаправление: ключ на другой адрес не уходит")
)

// maxBody — предел ответа: ряд по дням за год — десятки килобайт.
const maxBody = 8 << 20

// Client calls the API with a token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client // nil — свой, с минутным пределом
}

// Limits — запросы тарифа: доступно и потрачено.
type Limits struct {
	Available int `json:"available"`
	Used      int `json:"use"`
}

// Limits returns the plan's request limits: дешевый запрос, чтобы проверить ключ.
func (c Client) Limits(ctx context.Context) (Limits, error) {
	var l Limits
	return l, c.get(ctx, "/api/user/report_api_limit", nil, &l)
}

// DaySales — продажи категории за день, штук.
type DaySales struct {
	Day   time.Time // полночь UTC
	Sales float64
}

// CategoryByDay returns the Ozon category's sales for days from..to, старые сначала.
func (c Client) CategoryByDay(ctx context.Context, path string, from, to time.Time) ([]DaySales, error) {
	q := url.Values{"path": {path}, "d1": {from.Format(time.DateOnly)}, "d2": {to.Format(time.DateOnly)}, "groupBy": {"day"}}
	var rows []struct {
		Period string  `json:"period"`
		Sales  float64 `json:"sales"`
	}
	if err := c.get(ctx, "/api/oz/get/category/by_date", q, &rows); err != nil {
		return nil, err
	}
	out := make([]DaySales, 0, len(rows))
	for _, r := range rows {
		d, err := time.Parse(time.DateOnly, r.Period)
		if err != nil {
			return nil, fmt.Errorf("MPStats: день %q не прочитан", r.Period)
		}
		out = append(out, DaySales{Day: d, Sales: r.Sales})
	}
	slices.SortFunc(out, func(a, b DaySales) int { return a.Day.Compare(b.Day) })
	return out, nil
}

// get calls path with the token and decodes the answer into out.
func (c Client) get(ctx context.Context, path string, q url.Values, out any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	if !safeForKey(base) {
		return ErrInsecureURL
	}
	target := base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Mpstats-TOKEN", c.Token)
	client := &http.Client{Timeout: time.Minute}
	if c.HTTP != nil {
		cp := *c.HTTP
		client = &cp
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect } // ключ — только на BaseURL
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if err := statusErr(resp.StatusCode); err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// safeForKey — ключ уходит только по https; http — лишь на этот Мак (тесты).
func safeForKey(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (u.Scheme == "https" || (u.Scheme == "http" && loopback.Host(u.Hostname())))
}

// statusErr names a refusal; nil — ответ 200.
func statusErr(status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusTooManyRequests:
		return ErrQuota
	}
	return fmt.Errorf("MPStats ответил %d", status)
}
