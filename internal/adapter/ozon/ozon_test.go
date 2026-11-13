package ozon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
)

// fake answers like Seller API: курсор, has_next, лимит 100, ключи в заголовках.
func fake(t *testing.T, pages map[string][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "42" || r.Header.Get("Api-Key") != "secret" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":7,"message":"Invalid Api-Key"}`))
			return
		}
		var body struct {
			Cursor string `json:"cursor"`
			Limit  int    `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Limit > 100 && strings.Contains(r.URL.Path, "posting") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		list := pages[r.URL.Path]
		i := 0
		if body.Cursor != "" {
			i = int(body.Cursor[0] - '0')
		}
		if i >= len(list) {
			t.Errorf("%s: page %d asked, have %d", r.URL.Path, i, len(list))
			return
		}
		_, _ = w.Write([]byte(list[i]))
	}))
}

func TestClient_postingsAndStocks(t *testing.T) {
	srv := fake(t, map[string][]string{
		"/v3/posting/fbo/list": {
			`{"has_next":true,"cursor":"1","postings":[{"status":"delivered","products":[{"sku":11,"offer_id":"100_46","name":"Брюки","quantity":1,"price":{"amount":"1000","currency":"RUB"}}]}]}`,
			`{"has_next":false,"cursor":"","postings":[{"status":"cancelled","cancellation":{"cancel_reason":"Покупатель отказался при вручении: товар не подошел"},"products":[{"sku":11,"offer_id":"100_46","name":"Брюки","quantity":2,"price":{"amount":"990.50","currency":"RUB"}}]}]}`,
		},
		// FBS у старых версий API пишет цену строкой; другая валюта — без цены, валюты не смешиваются.
		// Названия — из карточек: у товара без заказов их больше негде взять.
		"/v3/product/info/list": {`{"items":[{"id":901,"offer_id":"100_46","name":"Брюки"},{"id":902,"offer_id":"200_L","name":"Футболка"}]}`},
		"/v4/posting/fbs/list":  {`{"has_next":false,"cursor":"","postings":[{"status":"delivered","products":[{"sku":22,"offer_id":"200_L","name":"Футболка","quantity":1,"price":"500.00"}]},{"status":"delivered","products":[{"sku":22,"offer_id":"200_L","name":"Футболка","quantity":1,"price":{"amount":"7","currency":"USD"}}]}]}`},
		"/v4/product/info/stocks": {
			`{"cursor":"1","total":2,"items":[{"product_id":901,"offer_id":"100_46","stocks":[{"type":"fbo","present":5,"reserved":1,"sku":11},{"type":"fbs","present":2,"reserved":0,"sku":11}]}]}`,
			`{"cursor":"","total":2,"items":[{"product_id":902,"offer_id":"200_L","stocks":[{"type":"fbo","present":3,"reserved":0,"sku":22}]}]}`,
		},
	})
	defer srv.Close()
	c := ozon.Client{BaseURL: srv.URL, ClientID: "42", Key: "secret", HTTP: srv.Client()}
	from := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	posts, err := c.Postings(context.Background(), from, from.AddDate(0, 0, 89))
	if err != nil {
		t.Fatalf("Postings: %v", err)
	}
	if len(posts) != 4 || posts[2].Scheme != "FBS" || posts[2].Items[0].Price != 500 || posts[3].Items[0].Price != 0 {
		t.Errorf("FBS prices = %+v; want 500 from a string and none for USD", posts[2:])
	}
	if len(posts) != 4 || posts[0].Scheme != "FBO" || posts[1].Status != "cancelled" || !strings.Contains(posts[1].CancelReason, "не подошел") {
		t.Fatalf("posts = %+v", posts)
	}
	if it := posts[1].Items[0]; it.SKU != "11" || it.Article != "100_46" || it.Qty != 2 || it.Price != 990.5 {
		t.Errorf("item = %+v", it)
	}
	stocks, err := c.Stocks(context.Background())
	if err != nil {
		t.Fatalf("Stocks: %v", err)
	}
	if len(stocks) != 2 || stocks[0].SKU != "11" || stocks[0].Qty != 6 || stocks[1].Article != "200_L" || stocks[1].Name != "Футболка" {
		t.Errorf("stocks = %+v; want present minus reserved summed by sku", stocks)
	}
}

func TestClient_refused(t *testing.T) {
	srv := fake(t, nil)
	defer srv.Close()
	c := ozon.Client{BaseURL: srv.URL, ClientID: "42", Key: "wrong", HTTP: srv.Client()}
	if _, err := c.Stocks(context.Background()); !errors.Is(err, ozon.ErrUnauthorized) {
		t.Errorf("wrong key: err = %v, want ErrUnauthorized", err)
	}
	if _, err := (ozon.Client{BaseURL: "http://example.com", ClientID: "42", Key: "secret"}).Stocks(context.Background()); !errors.Is(err, ozon.ErrInsecureURL) {
		t.Errorf("http to a foreign host: err = %v, want ErrInsecureURL — the key goes only over https", err)
	}
}
