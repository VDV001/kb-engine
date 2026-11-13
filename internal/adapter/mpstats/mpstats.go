// Package mpstats reads niche analytics from the MPStats API (#580): продажи
// категории Ozon по дням — для «Прогноза» вместо выгрузки файлом.
package mpstats

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	ErrTimeout      = errors.New("MPStats не ответил вовремя")
	ErrDropped      = errors.New("MPStats оборвал соединение")
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

// chunkDays — дней в одном запросе: 210 дней за раз MPStats не успевает (504
// через 60 с), 30 дней — 8,5 с (замер 13.11). parallel — частей сразу.
const (
	chunkDays = 31
	parallel  = 4
)

// CategoryByDay returns the Ozon category's sales for days from..to, старые
// сначала; долгий ряд — частями по chunkDays, до parallel частей сразу.
func (c Client) CategoryByDay(ctx context.Context, path string, from, to time.Time) ([]DaySales, error) {
	var spans [][2]time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, chunkDays) {
		spans = append(spans, [2]time.Time{d, minTime(d.AddDate(0, 0, chunkDays-1), to)})
	}
	parts := make([][]DaySales, len(spans))
	errs := make([]error, len(spans))
	sem := make(chan struct{}, parallel)
	// Первая отказавшая часть отменяет остальные: не тратить тариф ради одной ошибки (ревью #605).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i, sp := range spans {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if parts[i], errs[i] = c.categorySpan(ctx, path, sp[0], sp[1]); errs[i] != nil {
				cancel()
			}
		})
	}
	wg.Wait()
	if err := firstErr(errs); err != nil {
		return nil, err
	}
	out := slices.Concat(parts...)
	slices.SortFunc(out, func(a, b DaySales) int { return a.Day.Compare(b.Day) })
	return out, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// firstErr — причина отказа: первая не «отменено» (отмена — следствие), иначе любая.
func firstErr(errs []error) error {
	var any error
	for _, e := range errs {
		if e != nil && !errors.Is(e, context.Canceled) {
			return e
		}
		any = cmp.Or(any, e)
	}
	return any
}

// categorySpan asks one part of the range.
func (c Client) categorySpan(ctx context.Context, path string, from, to time.Time) ([]DaySales, error) {
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
	return out, nil
}

// retries — сколько раз повторить обрыв соединения, 502 и 504: MPStats под
// нагрузкой рвет соединение (живая проверка 13.11); отказ ключа не повторяется.
const retries = 2

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
	client := &http.Client{Timeout: 90 * time.Second} // часть ряда — до 15 с; запас на загруженный MPStats
	if c.HTTP != nil {
		cp := *c.HTTP
		client = &cp
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect } // ключ — только на BaseURL
	for try := 0; ; try++ {
		data, err := c.once(ctx, client, target)
		if err == nil {
			return json.Unmarshal(data, out)
		}
		if !errors.Is(err, ErrDropped) || try == retries || ctx.Err() != nil { // таймаут клиента — 90 с, не повторяем (ревью #606)
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(try+1) * time.Second):
		}
	}
}

// transportErr names a failed request without its URL (адрес с путем ниши
// человеку не нужен): обрыв — ErrDropped (его повторяют), таймаут — ErrTimeout,
// нет сети, сертификат, отказ в соединении — своей причиной (ревью #606).
func transportErr(ctx context.Context, err error) error {
	var ue *url.Error
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ErrRedirect):
		return ErrRedirect
	case errors.As(err, &ue) && ue.Timeout():
		return ErrTimeout
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return ErrDropped
	case errors.As(err, &ue):
		return ue.Err
	}
	return err
}

// once makes one request; обрыв и 502/504 — ErrDropped/ErrTimeout, их повторяет get.
func (c Client) once(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Mpstats-TOKEN", c.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, transportErr(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, ErrDropped
	}
	return data, statusErr(resp.StatusCode)
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
	if status == http.StatusGatewayTimeout || status == http.StatusBadGateway {
		return ErrDropped // MPStats сам не дождался — повторяется, как обрыв
	}
	return fmt.Errorf("MPStats ответил %d", status)
}

// currentCategory — строка итога самой категории в ответе подкатегорий: не ниша.
const currentCategory = "Текущая категория"

// Subcategory — ниша категории за период: продажи и выручка за весь период,
// продавцы — с продажами.
type Subcategory struct {
	Name     string  `json:"name"`
	Sales    float64 `json:"sales"`
	Revenue  float64 `json:"revenue"`
	Sellers  int     `json:"sellers_with_sells"`
	AvgPrice float64 `json:"avg_price"`
}

// Subcategories returns the category's direct subcategories for days from..to
// and the category itself (self): у конечной ниши подкатегорий нет, есть она сама.
func (c Client) Subcategories(ctx context.Context, path string, from, to time.Time) (subs []Subcategory, self Subcategory, err error) {
	q := url.Values{"path": {path}, "d1": {from.Format(time.DateOnly)}, "d2": {to.Format(time.DateOnly)}}
	var rows []Subcategory
	if err := c.get(ctx, "/api/oz/get/category/subcategories", q, &rows); err != nil {
		return nil, Subcategory{}, err
	}
	for _, r := range rows {
		if r.Name == currentCategory {
			self = r
		} else {
			subs = append(subs, r)
		}
	}
	return subs, self, nil
}

// Categories returns the paths of all Ozon categories, по алфавиту.
func (c Client) Categories(ctx context.Context) ([]string, error) {
	var rows []struct {
		Path string `json:"path"`
	}
	if err := c.get(ctx, "/api/oz/get/categories", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Path != "" {
			out = append(out, r.Path)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// TopSellersRevenue sums the revenue of the n largest sellers of the category
// for days from..to: для доли топ-n в оценке ниши.
func (c Client) TopSellersRevenue(ctx context.Context, path string, from, to time.Time, n int) (float64, error) {
	q := url.Values{"path": {path}, "d1": {from.Format(time.DateOnly)}, "d2": {to.Format(time.DateOnly)}}
	var rows []struct {
		Revenue float64 `json:"revenue"`
	}
	if err := c.get(ctx, "/api/oz/get/category/sellers", q, &rows); err != nil {
		return 0, err
	}
	rev := make([]float64, 0, len(rows))
	for _, r := range rows {
		rev = append(rev, r.Revenue)
	}
	slices.SortFunc(rev, func(a, b float64) int { return cmp.Compare(b, a) })
	var sum float64
	for _, v := range rev[:min(n, len(rev))] {
		sum += v
	}
	return sum, nil
}
