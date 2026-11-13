// Package ozon reads a seller's cabinet through Ozon Seller API (#580):
// только чтение — отправления FBO и FBS и остатки. Ключ уходит только на
// BaseURL по https (или на этот Мак — тесты).
package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// DefaultURL — адрес Seller API.
const DefaultURL = "https://api-seller.ozon.ru"

// Ошибки API.
var (
	ErrUnauthorized = errors.New("Client-Id или ключ не приняты")
	ErrInsecureURL  = errors.New("ключ Ozon уходит только по https")
	ErrTooMany      = errors.New("слишком частые запросы")
	ErrTooBig       = errors.New("отправлений больше 50 000 за период: возьмите период короче")
)

// pageLimit — страница отправлений: больше 100 Ozon не отдает.
const pageLimit = 100

// maxPages — предохранитель от курсора, который не кончается.
// ponytail: 500 страниц по 100 — 50 000 отправлений за период; потолок —
// крупный магазин за квартал; путь — запрос по месяцам.
const maxPages = 500

// retries — сколько раз ждать при «слишком часто» (429).
const retries = 4

// Client reads one cabinet. HTTP nil — клиент с минутой на запрос.
type Client struct {
	BaseURL, ClientID, Key string
	HTTP                   *http.Client
}

// Postings returns FBO and FBS postings created from the start of from to
// the end of to.
func (c Client) Postings(ctx context.Context, from, to time.Time) ([]markets.OzonPosting, error) {
	var out []markets.OzonPosting
	for _, s := range []struct{ scheme, path string }{{"FBO", "/v3/posting/fbo/list"}, {"FBS", "/v4/posting/fbs/list"}} {
		cursor := ""
		for page := 0; ; page++ {
			if page == maxPages {
				return nil, ErrTooBig
			}
			body := map[string]any{"dir": "ASC", "limit": pageLimit, "filter": map[string]string{
				"since": from.UTC().Format(time.RFC3339), "to": to.UTC().Add(24*time.Hour - time.Second).Format(time.RFC3339)}}
			if cursor != "" {
				body["cursor"] = cursor
			}
			var res postingPage
			if err := c.call(ctx, s.path, body, &res); err != nil {
				return nil, fmt.Errorf("отправления %s: %w", s.scheme, err)
			}
			for _, p := range res.Postings {
				out = append(out, p.posting(s.scheme))
			}
			if !res.HasNext || res.Cursor == "" || res.Cursor == cursor {
				break
			}
			cursor = res.Cursor
		}
	}
	return out, nil
}

// Stocks returns stock to sell by SKU: на складах минус в резерве, FBO и FBS
// вместе, с названием из карточки — у товара без заказов его больше негде взять.
func (c Client) Stocks(ctx context.Context) ([]markets.OzonStock, error) {
	var out []markets.OzonStock
	var ids []int64
	at := map[string]int{}
	cursor := ""
	for page := 0; ; page++ {
		if page == maxPages {
			return nil, ErrTooBig
		}
		var res stockPage
		body := map[string]any{"filter": map[string]string{"visibility": "ALL"}, "limit": 1000, "cursor": cursor}
		if err := c.call(ctx, "/v4/product/info/stocks", body, &res); err != nil {
			return nil, fmt.Errorf("остатки: %w", err)
		}
		for _, it := range res.Items {
			ids = append(ids, it.ProductID)
			for _, s := range it.Stocks {
				sku := strconv.FormatInt(s.SKU, 10)
				i, ok := at[sku]
				if !ok {
					i = len(out)
					at[sku] = i
					out = append(out, markets.OzonStock{SKU: sku, Article: it.OfferID})
				}
				out[i].Qty += float64(s.Present - s.Reserved)
			}
		}
		if res.Cursor == "" || res.Cursor == cursor || len(res.Items) == 0 {
			break
		}
		cursor = res.Cursor
	}
	names, err := c.names(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("карточки товаров: %w", err)
	}
	for i := range out {
		out[i].Name = names[out[i].Article]
	}
	return out, nil
}

// infoBatch — карточек за запрос: больше 1000 Ozon не принимает.
const infoBatch = 1000

// names returns card names by offer_id.
func (c Client) names(ctx context.Context, ids []int64) (map[string]string, error) {
	out := map[string]string{}
	for len(ids) > 0 {
		n := min(len(ids), infoBatch)
		var res struct {
			Items []struct {
				OfferID string `json:"offer_id"`
				Name    string `json:"name"`
			} `json:"items"`
		}
		if err := c.call(ctx, "/v3/product/info/list", map[string]any{"product_id": ids[:n]}, &res); err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			out[it.OfferID] = it.Name
		}
		ids = ids[n:]
	}
	return out, nil
}

type postingPage struct {
	HasNext  bool          `json:"has_next"`
	Cursor   string        `json:"cursor"`
	Postings []postingJSON `json:"postings"`
}

type postingJSON struct {
	Status       string `json:"status"`
	Cancellation *struct {
		Reason string `json:"cancel_reason"`
	} `json:"cancellation"`
	Products []struct {
		SKU      int64  `json:"sku"`
		OfferID  string `json:"offer_id"`
		Name     string `json:"name"`
		Quantity int    `json:"quantity"`
		Price    price  `json:"price"`
	} `json:"products"`
}

// price is a posting price in rubles: v3 пишет {"amount","currency"}, старые
// версии — строкой. Другая валюта — цены нет (0): валюты окно не смешивает.
type price float64

func (p *price) UnmarshalJSON(b []byte) error {
	var v struct{ Amount, Currency string }
	var s string
	switch {
	case json.Unmarshal(b, &s) == nil:
		v.Amount, v.Currency = s, "RUB"
	case json.Unmarshal(b, &v) != nil:
		return fmt.Errorf("цена Ozon не прочитана: %s", b)
	}
	if !strings.EqualFold(v.Currency, "RUB") {
		*p = 0
		return nil
	}
	f, _ := strconv.ParseFloat(v.Amount, 64) // пустая цена — 0, не ошибка
	*p = price(f)
	return nil
}

func (p postingJSON) posting(scheme string) markets.OzonPosting {
	out := markets.OzonPosting{Scheme: scheme, Status: p.Status}
	if p.Cancellation != nil {
		out.CancelReason = p.Cancellation.Reason
	}
	for _, it := range p.Products {
		out.Items = append(out.Items, markets.OzonItem{SKU: strconv.FormatInt(it.SKU, 10), Article: it.OfferID, Name: it.Name, Qty: it.Quantity, Price: float64(it.Price)})
	}
	return out
}

type stockPage struct {
	Cursor string `json:"cursor"`
	Items  []struct {
		ProductID int64  `json:"product_id"`
		OfferID   string `json:"offer_id"`
		Stocks    []struct {
			SKU      int64 `json:"sku"`
			Present  int   `json:"present"`
			Reserved int   `json:"reserved"`
		} `json:"stocks"`
	} `json:"items"`
}

// safeForKey reports whether the key may go to base: https или этот Мак.
func safeForKey(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (u.Scheme == "https" || (u.Scheme == "http" && loopback.Host(u.Hostname())))
}

// call posts body to path and decodes the answer into out; 429 — ждет и
// повторяет, 401 и 403 — ErrUnauthorized, другой отказ — с текстом Ozon.
func (c Client) call(ctx context.Context, path string, body, out any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	if !safeForKey(base) {
		return ErrInsecureURL
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	for try := 0; ; try++ {
		status, data, err := c.post(ctx, client, base+path, raw)
		if err != nil {
			return err
		}
		if status == http.StatusTooManyRequests && try < retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(try+1) * time.Second):
			}
			continue
		}
		if err := statusErr(status, data); err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	}
}

// statusErr names a refusal; nil — ответ 200.
func statusErr(status int, data []byte) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusTooManyRequests:
		return ErrTooMany
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	}
	return fmt.Errorf("ответ %d: %s", status, apiMessage(data))
}

// post sends one request with the cabinet's headers.
func (c Client) post(ctx context.Context, client *http.Client, u string, raw []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Client-Id", c.ClientID)
	req.Header.Set("Api-Key", c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, data, err
}

// apiMessage takes Ozon's own words from an error answer.
func apiMessage(data []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &e) == nil && e.Message != "" {
		return e.Message
	}
	return strings.TrimSpace(string(data[:min(len(data), 200)]))
}

// ErrRedirect is not written yet.
var ErrRedirect = errors.New("redirect")
