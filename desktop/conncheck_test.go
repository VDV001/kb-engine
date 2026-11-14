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
	if _, err := s.addMPStats(context.Background(), ep, "MPStats", " ", false); err == nil {
		t.Error("empty key saved")
	}
	if _, err := s.addMPStats(context.Background(), ep, "MPStats", "чужой", false); err == nil || !strings.Contains(err.Error(), "не принят") {
		t.Errorf("wrong key: %v", err)
	}
	if len(s.snapshot().cfg.Connections) != 0 || len(tok.m) != 0 {
		t.Fatalf("refused key left traces: %+v %v", s.snapshot().cfg.Connections, tok.m)
	}
	row, err := s.addMPStats(context.Background(), ep, " ", " m-1 ", false)
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
	mpRow, err := s.addMPStats(context.Background(), ep, "MPStats", "m-1", false)
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
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false)
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
	row, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false)
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
	// Подключение в запомненном источнике удалили, а другое есть — берется оно (живая проверка 13.11).
	if _, err := s.forecast(context.Background(), "", srv.Client(), "mpstats:старое|Одежда/Куртки", "mpstats:старое|Одежда/Куртки", own, today); err != nil {
		t.Errorf("stale connection id: %v", err)
	}
	if _, err := s.forecast(context.Background(), "", srv.Client(), own, src, own, today); err == nil || !strings.Contains(err.Error(), "обе ниши") {
		t.Errorf("file + MPStats: %v", err)
	}
}

// «Ниша → Из MPStats» (решение владельца 13.11): подкатегории выбранной
// категории за период и те же дни год назад — рост к году и оценка 0-100.
func TestNichesMPStats(t *testing.T) {
	s, _, _ := newServerSettings(t)
	if _, err := s.nichesMPStats(context.Background(), nil, "Одежда", "2026-10-14", "2026-11-12", time.Now()); err == nil || !strings.Contains(err.Error(), "MPStats") {
		t.Errorf("no connection: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/category/subcategories":
			if r.URL.Query().Get("path") == "Одежда/Куртки" {
				_, _ = w.Write([]byte(`[{"name":"Текущая категория","sales":150,"revenue":3000,"sellers_with_sells":7}]`))
				return
			}
			if r.URL.Query().Get("d1") == "2025-08-14" { // год назад у MPStats данных нет
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("d1") == "2025-10-14" {
				_, _ = w.Write([]byte(`[{"name":"Куртки","sales":100,"revenue":1000,"sellers_with_sells":5}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"name":"Текущая категория","sales":1},{"name":"Куртки","sales":150,"revenue":3000,"sellers_with_sells":7},{"name":"Шарфы","sales":30,"revenue":300,"sellers_with_sells":2}]`))
		case "/api/oz/get/category/sellers":
			if r.URL.Query().Get("d1") == "2026-08-14" { // период «без года назад»: продавцов тоже нет
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`[{"revenue":1500},{"revenue":100}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	v, err := s.nichesMPStats(context.Background(), srv.Client(), "Одежда", "2026-10-14", "2026-11-12", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if v.Leaf || v.File != "MPStats · Одежда" || v.Period != 30 || len(v.Niches) != 2 || v.Niches[0].Name != "Одежда/Куртки" || v.Niches[0].Values["growth"] != 0.5 ||
		v.Niches[0].Values["top10"] != 1600.0/3000 || !v.Niches[0].Score.Ok {
		t.Errorf("view = %+v", v)
	}
	// Конечная ниша без подкатегорий — в таблице она сама (живая проверка 13.11: «Пижамы женские»).
	if v, err := s.nichesMPStats(context.Background(), srv.Client(), "Одежда/Куртки", "2026-10-14", "2026-11-12", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)); err != nil || len(v.Niches) != 1 || v.Niches[0].Name != "Одежда/Куртки" || !v.Leaf {
		// Leaf: «Внутрь» на конечной нише не меняет таблицу, а говорит, что внутри ниш нет (живая проверка 14.11).
		t.Errorf("leaf: %v, %+v, leaf %v", err, v.Niches, v.Leaf)
	}
	// Путь, скопированный с вида «Одежда / Куртки», — тот же путь (живая проверка 13.11).
	if v, err := s.nichesMPStats(context.Background(), srv.Client(), " Одежда / ", "2026-10-14", "2026-11-12", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)); err != nil || v.File != "MPStats · Одежда" {
		t.Errorf("spaced path: %v, %q", err, v.File)
	}
	// Год назад не отдан — ниши без роста и с пояснением; исчерпанный лимит у
	// продавцов назван причиной (ревью #606).
	v2, err := s.nichesMPStats(context.Background(), srv.Client(), "Одежда", "2026-08-14", "2026-09-12", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if err != nil || len(v2.Niches) != 2 || v2.Niches[0].Values["growth"] != 0 {
		t.Errorf("without last year: %v, %+v", err, v2.Niches)
	}
	var nb strings.Builder
	for _, p := range v2.Problems {
		nb.WriteString(p.Reason + " | ")
	}
	notes := nb.String()
	if !strings.Contains(notes, "рост") || !strings.Contains(notes, "лимит") {
		t.Errorf("problems = %q; want the growth and the quota named", notes)
	}
	if got := mpstatsPath(" Одежда / Женская одежда /Пижамы женские/ "); got != "Одежда/Женская одежда/Пижамы женские" {
		t.Errorf("mpstatsPath = %q", got)
	}
	if _, err := s.nichesMPStats(context.Background(), srv.Client(), "Одежда", "2026-11-12", "2026-10-14", time.Now()); err == nil {
		t.Error("reversed period accepted")
	}
}
