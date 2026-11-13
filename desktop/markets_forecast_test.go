package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// forecastFiles — прогноз по трем файлам тем же путем, что в окне.
func forecastFiles(lastYear, nicheNow, own string) (forecastView, error) {
	return (&folderSettings{}).forecast(context.Background(), "", nil, lastYear, nicheNow, own, time.Now())
}

// longCSV пишет продажи по дням длинным файлом: дата;количество.
func longCSV(t *testing.T, name string, from, to time.Time, f func(time.Time) int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("Дата;Количество\n")
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		fmt.Fprintf(&b, "%s;%d\n", d.Format("02.01.2006"), f(d))
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestForecastFiles(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	last := longCSV(t, "прошлый.csv", at(2025, 9, 1), at(2025, 12, 31), func(d time.Time) int {
		switch {
		case d.Equal(at(2025, 11, 11)):
			return 600
		case d.Month() >= time.November:
			return 200
		}
		return 100
	})
	now := longCSV(t, "сейчас.csv", at(2026, 9, 1), at(2026, 10, 31), func(time.Time) int { return 150 })
	own := longCSV(t, "свои.csv", at(2026, 9, 1), at(2026, 10, 31), func(time.Time) int { return 15 })

	v, err := forecastFiles(last, now, own)
	if err != nil {
		t.Fatalf("forecastFiles: %v", err)
	}
	if len(v.Files) != 3 || v.Files[0].File != "прошлый.csv" || v.Files[0].Days != 122 || v.Files[0].From != "01.09.2025" || v.Files[2].Format != "long" {
		t.Errorf("files = %+v", v.Files)
	}
	if len(v.Days) != 61 {
		t.Fatalf("days = %d, want 61", len(v.Days))
	}
	if v.Days[10].Day != "2026-11-11" || v.Days[10].Value != 90 || len(v.LastYear) != 61 {
		t.Errorf("days = %d, 11.11 = %+v, last year = %d", len(v.Days), v.Days[10], len(v.LastYear))
	}
	if v.Peak.Day != "2026-11-11" || v.Growth != 0.5 || v.Share != 0.1 {
		t.Errorf("peak = %+v, growth = %v, share = %v", v.Peak, v.Growth, v.Share)
	}
	if len(v.Months) != 2 || v.Months[0].Label != "ноябрь 2026" || v.Months[0].Value != 960 || v.Months[1].Value != 930 {
		t.Errorf("months = %+v", v.Months)
	}

	_, err = forecastFiles(now, now, own)
	if !errors.Is(err, domain.ErrForecastInput) {
		t.Errorf("last year = this year: err = %v, want ErrForecastInput", err)
	}
	if _, err := forecastFiles("", now, own); err == nil {
		t.Error("missing file path accepted")
	}
}

// Свои заказы из кабинета Ozon вместо третьего файла (#580): тот же расчет,
// окно — 90 дней до вчера, заказы включая отмены (ревью #592).
func TestForecastOzon(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	last := longCSV(t, "прошлый.csv", at(2025, 9, 1), at(2025, 12, 31), func(d time.Time) int {
		if d.Month() >= time.November {
			return 200
		}
		return 100
	})
	now := longCSV(t, "сейчас.csv", at(2026, 9, 1), at(2026, 10, 31), func(time.Time) int { return 150 })
	var posts []string
	item := `"products":[{"sku":11,"offer_id":"a","name":"Брюки","quantity":15,"price":{"amount":"100","currency":"RUB"}}]`
	for d := at(2026, 9, 1); !d.After(at(2026, 10, 31)); d = d.AddDate(0, 0, 1) {
		ts := d.Add(9 * time.Hour).Format(time.RFC3339)
		posts = append(posts, `{"status":"delivered","created_at":"`+ts+`",`+item+`}`,
			`{"status":"cancelled","created_at":"`+ts+`",`+item+`}`)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Api-Key") != "k-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/v3/posting/fbo/list":
			_, _ = w.Write([]byte(`{"has_next":false,"postings":[` + strings.Join(posts, ",") + `]}`))
		default:
			_, _ = w.Write([]byte(`{"has_next":false,"postings":[]}`))
		}
	}))
	defer srv.Close()
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Пижамы", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.forecast(context.Background(), srv.URL, srv.Client(), last, now, "ozon:"+row.ID, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if len(v.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(v.Files))
	}
	if v.Files[2].File != "Ozon · Пижамы" || v.Files[2].Format != "ozon" || v.Files[2].To != "31.10.2026" || v.Files[2].Days != 90 {
		t.Errorf("own = %+v", v.Files[2])
	}
	if v.Share != 0.2 {
		t.Errorf("share = %v, want 0.2 (30 orders of 150 with cancels)", v.Share)
	}
	if !slices.Contains(s.Connections()[len(s.Connections())-1].UsedBy, "Маркеты → Прогноз") {
		t.Errorf("connection used by = %v, want the forecast too", s.Connections()[len(s.Connections())-1].UsedBy)
	}
}
