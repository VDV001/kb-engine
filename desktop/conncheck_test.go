package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// fakeMPStats answers like the MPStats API: ключ m-1, лимит 4 из 10 000.
func fakeMPStats(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mpstats-TOKEN") != "m-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"available":10000,"use":4}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Ключ MPStats — подключение, как кабинет Ozon (отзыв владельца 13.11): ключ
// проверяется до сохранения и лежит только в Связке ключей.
func TestAddMPStats(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	srv := fakeMPStats(t)
	ep := checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}
	if _, err := s.addMPStats(context.Background(), ep, "MPStats", " "); err == nil {
		t.Error("empty key saved")
	}
	if _, err := s.addMPStats(context.Background(), ep, "MPStats", "чужой"); err == nil || !strings.Contains(err.Error(), "не принят") {
		t.Errorf("wrong key: %v", err)
	}
	if len(s.snapshot().cfg.Connections) != 0 || len(tok.m) != 0 {
		t.Fatalf("refused key left traces: %+v %v", s.snapshot().cfg.Connections, tok.m)
	}
	row, err := s.addMPStats(context.Background(), ep, " ", " m-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "mpstats" || row.Name != "MPStats" || !row.OK || tok.m[connKeyAccount(row.ID)] != "m-1" {
		t.Errorf("row = %+v, key %q", row, tok.m[connKeyAccount(row.ID)])
	}
	if c, _ := s.snapshot().cfg.Connection(row.ID); c.Kind != desktopconfig.ConnMPStats || strings.Contains(c.Name+c.Account+c.URL, "m-1") {
		t.Errorf("config = %+v; the key must not be there", c)
	}
}

// «Проверить» у каждого подключения (отзыв владельца 13.11): живой запрос туда,
// куда подключение ходит, и итог словами.
func TestCheckConnection(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	oz, mp := fakeOzon(t), fakeMPStats(t)
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(speech.Close)
	ep := checkEndpoints{Ozon: oz.URL, MPStats: mp.URL, HTTP: oz.Client()}

	ozRow, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	mpRow, err := s.addMPStats(context.Background(), ep, "MPStats", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	spRow, err := s.AddSpeech("whisper", speech.URL, "", "s-1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, id string
		ok       bool
		note     string
	}{
		{"кабинет Ozon", ozRow.ID, true, "ключ принят"},
		{"MPStats", mpRow.ID, true, "4 из 10"},
		{"распознавание речи", spRow.ID, true, "отвечает"},
		{"нет такого", "нет-такого", false, "не найдено"},
	}
	for _, tc := range tests {
		got := s.checkConn(context.Background(), tc.id, ep)
		if got.OK != tc.ok || !strings.Contains(got.Note, tc.note) {
			t.Errorf("%s: %+v, want ok=%v and %q", tc.name, got, tc.ok, tc.note)
		}
	}
	tok.m[connKeyAccount(ozRow.ID)] = "чужой"
	tok.m[connKeyAccount(mpRow.ID)] = "чужой"
	tok.m[connKeyAccount(spRow.ID)] = "чужой"
	for _, id := range []string{ozRow.ID, mpRow.ID, spRow.ID} {
		if got := s.checkConn(context.Background(), id, ep); got.OK || !strings.Contains(got.Note, "не принят") {
			t.Errorf("%s with a wrong key: %+v", id, got)
		}
	}
}

// «Прогноз» берет нишу из MPStats (отзыв владельца 13.11): обе ниши — одним
// источником «mpstats:<id>|<путь>»: сейчас — 90 дней до вчера, прошлый год —
// те же дни годом раньше и 120 дней после них.
func TestForecastMPStats(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var mu sync.Mutex
	asked := map[string]bool{} // запрошенные дни: ряд идет частями, параллельно
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mpstats-TOKEN") != "m-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/user/report_api_limit" {
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
			return
		}
		q := r.URL.Query()
		if q.Get("path") != "Одежда/Куртки" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		d1, _ := time.Parse(time.DateOnly, q.Get("d1"))
		d2, _ := time.Parse(time.DateOnly, q.Get("d2"))
		mu.Lock()
		for d := d1; !d.After(d2); d = d.AddDate(0, 0, 1) {
			asked[d.Format(time.DateOnly)] = true
		}
		mu.Unlock()
		var b strings.Builder
		b.WriteString("[")
		for d := d1; !d.After(d2); d = d.AddDate(0, 0, 1) {
			if d != d1 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"period":%q,"sales":100}`, d.Format(time.DateOnly))
		}
		b.WriteString("]")
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	own := longCSV(t, "свои.csv", time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), func(time.Time) int { return 10 })
	src := "mpstats:" + row.ID + "|Одежда/Куртки"
	v, err := s.forecast(context.Background(), "", srv.Client(), src, src, own, today)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	// Прошлый год — 15.08.2025…12.03.2026 (210 дней), сейчас — 15.08…12.11.2026 (90).
	if len(asked) != 210+90 || !asked["2025-08-15"] || !asked["2026-03-12"] || asked["2026-03-13"] || !asked["2026-08-15"] || !asked["2026-11-12"] || asked["2026-11-13"] {
		t.Errorf("asked %d days", len(asked))
	}
	if len(v.Files) != 3 || !strings.Contains(v.Files[0].File, "MPStats") || v.Files[1].Days != 90 || len(v.Days) == 0 || v.Share != 0.1 {
		t.Errorf("files %+v, days %d, share %v", v.Files, len(v.Days), v.Share)
	}
	if _, err := s.forecast(context.Background(), "", srv.Client(), "mpstats:"+row.ID+"|Нет/Такой", "mpstats:"+row.ID+"|Нет/Такой", own, today); err == nil || !strings.Contains(err.Error(), "Нет/Такой") {
		t.Errorf("unknown niche: %v", err)
	}
}

// «Проверить» зеленое только на настоящий ответ: 404, 429 и перенаправление —
// не «сервер отвечает»; ключа нет в Связке — так и сказано (ревью #605).
func TestCheckConnection_honest(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	for _, tc := range []struct {
		status int
		note   string
	}{{http.StatusNotFound, "путь"}, {http.StatusTooManyRequests, "занят"}, {http.StatusFound, "путь"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.status == http.StatusFound {
				w.Header().Set("Location", "/elsewhere")
			}
			w.WriteHeader(tc.status)
		}))
		row, err := s.AddSpeech("whisper", srv.URL, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := s.checkConn(context.Background(), row.ID, checkEndpoints{HTTP: srv.Client()}); got.OK || !strings.Contains(got.Note, tc.note) {
			t.Errorf("status %d: %+v, want not ok with %q", tc.status, got, tc.note)
		}
		srv.Close()
	}
	oz, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	delete(tok.m, connKeyAccount(oz.ID))
	if got := s.checkConn(context.Background(), oz.ID, checkEndpoints{}); got.OK || !strings.Contains(got.Note, "нет в Связке") {
		t.Errorf("missing key: %+v", got)
	}
}

// Ряд MPStats за те же дни берется из памяти: «Посчитать», «Посчитать
// моделью» и пересчет со сменой своего файла не тратят тариф заново (ревью
// #605). Ниша «сейчас» из MPStats при файле прошлого года — понятный отказ.
func TestForecastMPStats_cachedAndGuarded(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/report_api_limit" {
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
			return
		}
		mu.Lock()
		n++
		mu.Unlock()
		q := r.URL.Query()
		d1, _ := time.Parse(time.DateOnly, q.Get("d1"))
		d2, _ := time.Parse(time.DateOnly, q.Get("d2"))
		var parts []string
		for d := d1; !d.After(d2); d = d.AddDate(0, 0, 1) {
			parts = append(parts, fmt.Sprintf(`{"period":%q,"sales":100}`, d.Format(time.DateOnly)))
		}
		_, _ = w.Write([]byte("[" + strings.Join(parts, ",") + "]"))
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	own := longCSV(t, "свои.csv", time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), func(time.Time) int { return 10 })
	src := "mpstats:" + row.ID + "|Одежда/Куртки"
	for range 2 {
		if _, err := s.forecast(context.Background(), "", srv.Client(), src, src, own, today); err != nil {
			t.Fatal(err)
		}
	}
	if n != 10 {
		t.Errorf("%d requests for two forecasts, want 10 (7 + 3, once)", n)
	}
	if _, err := s.forecast(context.Background(), "", srv.Client(), own, src, own, today); err == nil || !strings.Contains(err.Error(), "обе ниши") {
		t.Errorf("file + MPStats: %v", err)
	}
}
