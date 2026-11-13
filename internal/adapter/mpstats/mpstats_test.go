package mpstats_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
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

// Долгий ряд — частями по 31 дню (живой замер 13.11: 210 дней за раз —
// 504 через 60 с у MPStats, 30 дней — 8,5 с), склеенными по порядку.
func TestCategoryByDay_chunks(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		asked = append(asked, q.Get("d1")+"…"+q.Get("d2"))
		mu.Unlock()
		d1, _ := time.Parse(time.DateOnly, q.Get("d1"))
		d2, _ := time.Parse(time.DateOnly, q.Get("d2"))
		var parts []string
		for d := d1; !d.After(d2); d = d.AddDate(0, 0, 1) {
			parts = append(parts, fmt.Sprintf(`{"period":%q,"sales":%d}`, d.Format(time.DateOnly), d.YearDay()))
		}
		_, _ = w.Write([]byte("[" + strings.Join(parts, ",") + "]"))
	}))
	t.Cleanup(srv.Close)
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	days, err := mpstats.Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}.CategoryByDay(context.Background(), "x", from, from.AddDate(0, 0, 69))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(asked)
	if want := []string{"2025-01-01…2025-01-31", "2025-02-01…2025-03-03", "2025-03-04…2025-03-11"}; !slices.Equal(asked, want) {
		t.Errorf("asked %v, want %v", asked, want)
	}
	if len(days) != 70 || days[0].Sales != 1 || days[69].Sales != 70 {
		t.Errorf("days = %d, first %v, last %v", len(days), days[0], days[len(days)-1])
	}
}

// Первая отказавшая часть отменяет остальные: отозванный ключ или исчерпанный
// лимит не тратит тариф на семь запросов ради одной ошибки (ревью #605).
func TestCategoryByDay_stopsOnFirstError(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := mpstats.Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.CategoryByDay(context.Background(), "x", from, from.AddDate(0, 0, 209)); !errors.Is(err, mpstats.ErrQuota) {
		t.Fatalf("err = %v, want ErrQuota", err)
	}
	mu.Lock() // отмененные части сервер еще может дослушивать
	defer mu.Unlock()
	if n > 4 {
		t.Errorf("%d requests after the first refusal, want at most the first round of 4", n)
	}
}

// Ниши категории за период (живой ответ 13.11): строка «Текущая категория» —
// итог самой категории, не ниша; продавцы — с продажами.
func TestSubcategories(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/oz/get/category/subcategories" || q.Get("path") != "Одежда" || q.Get("d1") != "2026-10-14" || q.Get("d2") != "2026-11-12" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[{"name":"Текущая категория","sales":10,"revenue":100,"sellers_with_sells":5},` +
			`{"name":"Женская одежда","sales":6,"revenue":60,"sellers_with_sells":3,"avg_price":12},{"name":"Мужская одежда","sales":4,"revenue":40,"sellers_with_sells":2}]`))
	}))
	t.Cleanup(srv.Close)
	subs, err := mpstats.Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}.Subcategories(context.Background(), "Одежда",
		time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 || subs[0].Name != "Женская одежда" || subs[0].Sales != 6 || subs[0].Revenue != 60 || subs[0].Sellers != 3 {
		t.Errorf("subs = %+v", subs)
	}
}

// Пути всех категорий Ozon — подсказки поля «Категория».
func TestCategories(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"url":"/category/a","path":"Одежда/Женская одежда","name":"Женская одежда"},{"url":"/category/b","path":"Одежда","name":"Одежда"}]`))
	}))
	t.Cleanup(srv.Close)
	paths, err := mpstats.Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}.Categories(context.Background())
	if err != nil || !slices.Equal(paths, []string{"Одежда", "Одежда/Женская одежда"}) {
		t.Errorf("paths = %v, %v; want sorted", paths, err)
	}
}

// Выручка десяти крупнейших продавцов ниши — для доли топ-10 в оценке.
func TestTopSellersRevenue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oz/get/category/sellers" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var parts []string
		for i := 1; i <= 12; i++ {
			parts = append(parts, fmt.Sprintf(`{"supplierid":%d,"revenue":%d}`, i, i*10))
		}
		_, _ = w.Write([]byte("[" + strings.Join(parts, ",") + "]"))
	}))
	t.Cleanup(srv.Close)
	d := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	got, err := mpstats.Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}.TopSellersRevenue(context.Background(), "x", d, d, 10)
	if want := float64(30 + 40 + 50 + 60 + 70 + 80 + 90 + 100 + 110 + 120); err != nil || got != want {
		t.Errorf("top 10 = %v, %v; want %v (the largest ten, whatever the order)", got, err, want)
	}
}

// Обрыв соединения и 504 у MPStats повторяются (живая проверка 13.11:
// «connection reset by peer» при расчете моделью); отказ ключа — нет.
func TestRetriesTransient(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		if k == 1 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close() // обрыв без ответа
			return
		}
		if k == 2 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_, _ = w.Write([]byte(`{"available":1,"use":0}`))
	}))
	t.Cleanup(srv.Close)
	if l, err := (mpstats.Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}).Limits(context.Background()); err != nil || l.Available != 1 {
		t.Errorf("after a reset and a 504: %+v, %v", l, err)
	}
}
