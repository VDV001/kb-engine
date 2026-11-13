package mpstats_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
)

// fake answers like the MPStats API (живой ответ 13.11): ключ в X-Mpstats-TOKEN.
func fake(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mpstats-TOKEN") != "t-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401,"message":"Authorization Required"}`))
			return
		}
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10000,"use":4,"available_ozon_external":10000,"use_ozon_external":4}`))
		case "/api/oz/get/category/by_date":
			q := r.URL.Query()
			if q.Get("path") != "Одежда/Куртки" || q.Get("d1") != "2025-11-01" || q.Get("d2") != "2025-11-03" || q.Get("groupBy") != "day" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`[{"period":"2025-11-03","sales":22034,"revenue":136235247},{"period":"2025-11-02","sales":15000.5,"revenue":1},{"period":"2025-11-01","sales":0,"revenue":0}]`))
		case "/api/oz/get/category/limited":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLimits(t *testing.T) {
	srv := fake(t)
	l, err := mpstats.Client{BaseURL: srv.URL, Token: "t-1", HTTP: srv.Client()}.Limits(context.Background())
	if err != nil || l.Available != 10000 || l.Used != 4 {
		t.Fatalf("limits = %+v, %v", l, err)
	}
	if _, err := (mpstats.Client{BaseURL: srv.URL, Token: "чужой", HTTP: srv.Client()}).Limits(context.Background()); !errors.Is(err, mpstats.ErrUnauthorized) {
		t.Errorf("wrong token: %v, want ErrUnauthorized", err)
	}
	if _, err := (mpstats.Client{BaseURL: "http://mpstats.example", Token: "t-1"}).Limits(context.Background()); !errors.Is(err, mpstats.ErrInsecureURL) {
		t.Errorf("http: %v, want ErrInsecureURL (ключ — только по https)", err)
	}
}

func TestCategoryByDay(t *testing.T) {
	srv := fake(t)
	c := mpstats.Client{BaseURL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	days, err := c.CategoryByDay(context.Background(), "Одежда/Куртки", time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 || !days[0].Day.Equal(time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)) || days[2].Sales != 22034 || days[1].Sales != 15000.5 {
		t.Errorf("days = %+v; want three days oldest first", days)
	}
}
