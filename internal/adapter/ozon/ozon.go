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
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// DefaultURL — адрес Seller API.
const DefaultURL = "https://api-seller.ozon.ru"

// Ошибки API.
var (
	ErrUnauthorized = errors.New("Client-Id или ключ не приняты")
	ErrInsecureURL  = errors.New("ключ Ozon уходит только по https")
	ErrTooMany      = errors.New("слишком частые запросы")
	ErrTooBig       = errors.New("слишком много страниц ответа")
	ErrRedirect     = errors.New("перенаправление: ключ на другой адрес не уходит")
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
	acc := stockAcc{at: map[string]int{}}
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
		acc.add(res)
		if res.Cursor == "" || res.Cursor == cursor || len(res.Items) == 0 {
			break
		}
		cursor = res.Cursor
	}
	names, err := c.names(ctx, acc.ids)
	if err != nil {
		return nil, fmt.Errorf("карточки товаров: %w", err)
	}
	for i := range acc.out {
		acc.out[i].Name = names[acc.out[i].Article]
	}
	return acc.out, nil
}

// stockAcc sums stock pages by SKU and keeps product ids for card names.
type stockAcc struct {
	out []markets.OzonStock
	ids []int64
	at  map[string]int
}

func (a *stockAcc) add(res stockPage) {
	for _, it := range res.Items {
		a.ids = append(a.ids, it.ProductID)
		for _, s := range it.Stocks {
			if s.SKU == 0 {
				continue // товар еще без SKU на Ozon (модерация): не склеивать в один
			}
			sku := strconv.FormatInt(s.SKU, 10)
			i, ok := a.at[sku]
			if !ok {
				i = len(a.out)
				a.at[sku] = i
				a.out = append(a.out, markets.OzonStock{SKU: sku, Article: it.OfferID})
			}
			a.out[i].Qty += float64(s.Present - s.Reserved)
		}
	}
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
	Number       string `json:"posting_number"`
	Status       string `json:"status"`
	InProcessAt  string `json:"in_process_at"`
	CreatedAt    string `json:"created_at"`
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
	// День — по дате создания: по ней Ozon отбирает период (since/to), и края
	// окна не теряются (ревью #592). Пустая или чужая дата — без даты, а не
	// сломанная страница: «Магазину» даты не нужны, «Прогноз» их сосчитает.
	out := markets.OzonPosting{Number: p.Number, Scheme: scheme, Status: p.Status}
	for _, s := range []string{p.CreatedAt, p.InProcessAt} {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			out.At = t
			break
		}
	}
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
	client := &http.Client{Timeout: time.Minute}
	if c.HTTP != nil {
		cp := *c.HTTP
		client = &cp
	}
	// Go повторяет свои заголовки (Api-Key, Client-Id) на хост перенаправления:
	// ключ уходит только на BaseURL (ревью #590, как kbserver).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect }
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
	r := []rune(strings.TrimSpace(string(data)))
	return string(r[:min(len(r), 200)]) // по буквам: байтовая обрезка режет кириллицу
}

// ErrCurrency — начисление не в рублях: валюты окно не смешивает.
var ErrCurrency = errors.New("начисление не в рублях")

// Accruals returns the cabinet's accruals by day, from to to inclusive
// (/v1/finance/accrual/by-day; /v3/finance/transaction/* Ozon отключил 08.09.2026):
// продажа, вознаграждение и каждая услуга — своей строкой. Итог Ozon, не равный
// сумме частей, дополняется строкой разницы: на счет приходит итог.
func (c Client) Accruals(ctx context.Context, from, to time.Time) ([]markets.OzonAccrual, error) {
	var out []markets.OzonAccrual
	first := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	last := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		lastID := ""
		for page := 0; ; page++ {
			if page == maxPages {
				return nil, ErrTooBig
			}
			body := map[string]string{"date": day.Format(time.DateOnly)}
			if lastID != "" {
				body["last_id"] = lastID
			}
			var res accrualPage
			if err := c.call(ctx, "/v1/finance/accrual/by-day", body, &res); err != nil {
				return nil, fmt.Errorf("начисления за %s: %w", day.Format("02.01.2006"), err)
			}
			for _, a := range res.Accruals {
				rows, err := a.rows(day)
				if err != nil {
					return nil, fmt.Errorf("начисление %d: %w", a.ID, err)
				}
				out = append(out, rows...)
			}
			if res.LastID == "" || res.LastID == lastID {
				break
			}
			lastID = res.LastID
		}
	}
	return out, nil
}

type accrualPage struct {
	Accruals []accrualJSON `json:"accruals"`
	LastID   string        `json:"last_id"`
}

// amount is Ozon's money: сумма строкой и валюта.
type amount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// absent — суммы нет вовсе (поле пропущено или null): часть не считается,
// расхождение с итогом уйдет в строку разницы (ревью #594).
func (a amount) absent() bool { return a.Amount == "" && a.Currency == "" }

func (a amount) money() (domain.Money, error) {
	if !strings.EqualFold(a.Currency, "RUB") {
		return domain.Money{}, fmt.Errorf("%w: %q", ErrCurrency, a.Currency)
	}
	v := a.Amount
	if whole, frac, ok := strings.Cut(v, "."); ok && len(frac) > 2 {
		v = whole + "." + frac[:2] + strings.TrimRight(frac[2:], "0") // "-11.5000" — те же копейки; доли копейки — отказ
	}
	return domain.ParseMoney(v)
}

type feeJSON struct {
	TypeID  int    `json:"type_id"`
	Accrued amount `json:"accrued"`
}

type accrualJSON struct {
	ID       int64  `json:"accrual_id"`
	Unit     string `json:"unit_number"`
	Category string `json:"accrued_category"`
	Total    amount `json:"total_amount"`
	Posting  *struct {
		Products []struct {
			SKU      int64 `json:"sku"`
			Delivery *struct {
				Services []feeJSON `json:"services"`
			} `json:"delivery"`
			Commission *struct {
				SaleAmount amount `json:"sale_amount"`
				Commission amount `json:"commission"`
			} `json:"commission"`
		} `json:"products"`
	} `json:"posting"`
	ItemFees *struct {
		Fees []struct {
			SKU  int64     `json:"sku"`
			Fees []feeJSON `json:"fees"`
		} `json:"fees"`
	} `json:"item_fees"`
	NonItemFee *feeJSON `json:"non_item_fee"`
}

// part is one piece of an accrual before its amount is read.
type part struct {
	sku    int64
	kind   markets.AccrualKind
	typeID int
	v      amount
}

// parts lists the accrual's pieces: продажа, вознаграждение и услуги.
func (a accrualJSON) parts() []part {
	var out []part
	if a.Posting != nil {
		for _, p := range a.Posting.Products {
			if c := p.Commission; c != nil {
				out = append(out, part{p.SKU, markets.AccrualSale, 0, c.SaleAmount}, part{p.SKU, markets.AccrualCommission, 0, c.Commission})
			}
			if p.Delivery != nil {
				for _, f := range p.Delivery.Services {
					out = append(out, part{p.SKU, markets.AccrualService, f.TypeID, f.Accrued})
				}
			}
		}
	}
	if a.ItemFees != nil {
		for _, it := range a.ItemFees.Fees {
			for _, f := range it.Fees {
				out = append(out, part{it.SKU, markets.AccrualService, f.TypeID, f.Accrued})
			}
		}
	}
	if f := a.NonItemFee; f != nil {
		out = append(out, part{0, markets.AccrualService, f.TypeID, f.Accrued})
	}
	return out
}

// rows splits one accrual into lines of day; разница итога и частей — строкой.
func (a accrualJSON) rows(day time.Time) ([]markets.OzonAccrual, error) {
	total, err := a.Total.money()
	if err != nil {
		return nil, err
	}
	unit := "" // у NON_ITEM в unit_number — номер кампании или документа, не отправления
	if a.Category == "POSTING" || a.Category == "ITEM" {
		unit = a.Unit
	}
	var out []markets.OzonAccrual
	var sum domain.Money
	for _, p := range a.parts() {
		if p.v.absent() {
			continue
		}
		m, err := p.v.money()
		if err != nil {
			return nil, err
		}
		sku := ""
		if p.sku != 0 {
			sku = strconv.FormatInt(p.sku, 10)
		}
		out = append(out, markets.OzonAccrual{Day: day, SKU: sku, Unit: unit, Kind: p.kind, TypeID: p.typeID, Amount: m})
		sum = sum.Add(m)
	}
	if rest := total.Sub(sum); !rest.IsZero() {
		out = append(out, markets.OzonAccrual{Day: day, Unit: unit, Kind: markets.AccrualRest, Amount: rest})
	}
	return out, nil
}

// returnsLimit — возвратов за запрос: больше 500 Ozon не отдает.
const returnsLimit = 500

// Returns returns returns and non-pickups whose return started from the start
// of from to the end of to (/v1/returns/list, страницы по id последнего).
func (c Client) Returns(ctx context.Context, from, to time.Time) ([]markets.OzonReturn, error) {
	var out []markets.OzonReturn
	var lastID int64
	for page := 0; ; page++ {
		if page == maxPages {
			return nil, ErrTooBig
		}
		body := map[string]any{"limit": returnsLimit, "last_id": lastID, "filter": map[string]any{"logistic_return_date": map[string]string{
			"time_from": from.UTC().Format(time.RFC3339), "time_to": to.UTC().Add(24*time.Hour - time.Second).Format(time.RFC3339)}}}
		var res returnPage
		if err := c.call(ctx, "/v1/returns/list", body, &res); err != nil {
			return nil, fmt.Errorf("возвраты: %w", err)
		}
		for _, r := range res.Returns {
			out = append(out, r.ret())
		}
		if !res.HasNext || len(res.Returns) == 0 || res.Returns[len(res.Returns)-1].ID == lastID {
			break
		}
		lastID = res.Returns[len(res.Returns)-1].ID
	}
	return out, nil
}

type returnPage struct {
	Returns []returnJSON `json:"returns"`
	HasNext bool         `json:"has_next"`
}

type returnJSON struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Posting string `json:"posting_number"`
	Product struct {
		SKU   int64  `json:"sku"`
		Name  string `json:"name"`
		Price struct {
			Currency string  `json:"currency_code"`
			Price    float64 `json:"price"`
		} `json:"price"`
	} `json:"product"`
	Visual struct {
		Status struct {
			SysName string `json:"sys_name"`
		} `json:"status"`
		Changed string `json:"change_moment"`
	} `json:"visual"`
}

func (r returnJSON) ret() markets.OzonReturn {
	out := markets.OzonReturn{Posting: r.Posting, SKU: strconv.FormatInt(r.Product.SKU, 10), Name: r.Product.Name, Type: r.Type, Status: r.Visual.Status.SysName}
	if strings.EqualFold(r.Product.Price.Currency, "RUB") {
		out.Price = r.Product.Price.Price // другая валюта — без цены: валюты окно не смешивает
	}
	out.Changed, _ = time.Parse(time.RFC3339, r.Visual.Changed) // без даты — нулевое время: «застрял» не скажем
	return out
}
