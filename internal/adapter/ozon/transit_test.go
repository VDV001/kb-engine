package ozon_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
)

type transitTransport func(*http.Request) (*http.Response, error)

func (f transitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStockTransitBatchesAndSumsBySKU(t *testing.T) {
	var skus []string
	for i := 1; i <= 101; i++ {
		skus = append(skus, strconv.Itoa(i))
	}
	calls := 0
	httpClient := &http.Client{Transport: transitTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body struct{ SKUs []string }
		if r.URL.Path != "/v1/analytics/stocks" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatalf("bad request %s: %+v", r.URL.Path, body)
		}
		if calls == 1 && len(body.SKUs) != 100 || calls == 2 && len(body.SKUs) != 1 {
			t.Fatalf("batch %d has %d SKU", calls, len(body.SKUs))
		}
		payload := `{"items":[{"sku":1,"outbound_pending_delivery":2,"transit_stock_count":0,"requested_stock_count":99},{"sku":1,"outbound_pending_delivery":3,"transit_stock_count":0}]}`
		if calls == 2 {
			payload = `{"items":[{"sku":101,"transit_stock_count":4}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	got, err := (ozon.Client{BaseURL: ozon.DefaultURL, HTTP: httpClient}).StockTransit(t.Context(), skus)
	if err != nil || calls != 2 || len(got) != 2 {
		t.Fatalf("balances = %+v, calls = %d, err = %v", got, calls, err)
	}
	if p := got["1"]; p.ToCustomer == nil || *p.ToCustomer != 5 || p.ToWarehouse == nil || *p.ToWarehouse != 0 {
		t.Errorf("SKU 1 = %+v, want 5 to customers and 0 in supplies", p)
	}
	if p := got["101"]; p.ToCustomer != nil || p.ToWarehouse == nil || *p.ToWarehouse != 4 {
		t.Errorf("SKU 101 = %+v, want unknown to customers and 4 in supplies", p)
	}
}
