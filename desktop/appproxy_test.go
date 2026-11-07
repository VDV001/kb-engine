package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

// Прокси модуля «Финансы» (#435, G2): витрина под /app/ в окне, запросы
// /app/api|kb|media на kb-сервер с токеном из Связки. Только из обработчика
// Wails: loopback-порт отдал бы данные с токеном любому процессу Мака.

var appUI = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>финансы</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

func proxyTo(t *testing.T, up *httptest.Server, token string) http.Handler {
	t.Helper()
	return newAppProxy(func() (appUpstream, bool) { return appUpstream{URL: up.URL, Token: token}, true }, appUI, up.Client().Transport)
}

func TestAppProxy_addsTokenAndDropsBrowserCredentials(t *testing.T) {
	var got *http.Request
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer up.Close()

	req := httptest.NewRequest(http.MethodGet, "/app/api/finances?year=2026", nil)
	req.Header.Set("Cookie", "webview=1")
	req.Header.Set("Authorization", "Bearer from-page")
	req.Header.Set("X-KB-Token", "forged")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	proxyTo(t, up, "tok-1").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("ответ %d %q", rec.Code, rec.Body.String())
	}
	if got.URL.Path != "/api/finances" || got.URL.RawQuery != "year=2026" {
		t.Errorf("на сервер ушло %s?%s, want /api/finances?year=2026", got.URL.Path, got.URL.RawQuery)
	}
	if v := got.Header.Get("X-KB-Token"); v != "tok-1" {
		t.Errorf("X-KB-Token = %q, want токен из Связки, а не из страницы", v)
	}
	for _, h := range []string{"Cookie", "Authorization"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("%s от webview ушел на сервер: %q", h, v)
		}
	}
	if got.Header.Get("Accept") != "application/json" {
		t.Error("Accept не дошел до сервера")
	}
	if v := rec.Header().Get("Set-Cookie"); v != "" {
		t.Errorf("Set-Cookie сервера отдан webview: %q", v)
	}
}

// Перенаправление сервера не исполняется: токен не уходит туда, куда указал
// чужой Location.
func TestAppProxy_doesNotFollowRedirects(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer up.Close()

	rec := httptest.NewRecorder()
	proxyTo(t, up, "tok-1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/api/finances", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("код %d, want 502 на перенаправление", rec.Code)
	}
	if elsewhere.Load() != 0 {
		t.Error("прокси пошел по перенаправлению")
	}
}

// Токен — только на https или http этого Мака, как у источников (CheckServerURL).
func TestAppProxy_refusesPlainHTTPToTheNetwork(t *testing.T) {
	var calls atomic.Int32
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, io.EOF })
	p := newAppProxy(func() (appUpstream, bool) { return appUpstream{URL: "http://kb.example.ru", Token: "tok-1"}, true }, appUI, rt)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/api/finances", nil))
	if rec.Code != http.StatusBadGateway || calls.Load() != 0 {
		t.Fatalf("код %d, запросов в сеть %d: http в сеть с токеном пропущен", rec.Code, calls.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Без сервера — понятный экран и понятная ошибка API, а не пустая витрина.
func TestAppProxy_withoutAServer(t *testing.T) {
	p := newAppProxy(func() (appUpstream, bool) { return appUpstream{}, false }, appUI, nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "kb-сервер") {
		t.Errorf("страница без сервера: %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/api/finances", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "kb-сервер") {
		t.Errorf("API без сервера: %d %q", rec.Code, rec.Body.String())
	}
}

// Витрина: /app → /app/ (код грузится по ./assets/ от страницы), файлы — из
// сборки, незнакомый путь — index.html (маршруты витрины).
func TestAppProxy_servesTheUI(t *testing.T) {
	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	p := proxyTo(t, up, "tok-1")
	cases := []struct {
		path, wantLoc, wantBody string
		code                    int
	}{
		{"/app", "/app/", "", http.StatusMovedPermanently},
		{"/app/", "", "<title>финансы", http.StatusOK},
		{"/app/assets/app.js", "", "console.log", http.StatusOK},
		{"/app/accounts", "", "<title>финансы", http.StatusOK},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code || rec.Header().Get("Location") != tc.wantLoc || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s: %d Location=%q body=%q", tc.path, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	}
}

// G3 — запись через прокси (#435). Сервер защищается от CSRF по Origin, а
// прокси Origin не пересылает, поэтому защиту держит окно: запись — только
// JSON с заголовком витрины X-DVK-App (чужая страница без CORS его не
// поставит, форма — тем более) и не межсайтовая.
func TestAppProxy_write(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		appHeader   bool
		fetchSite   string
		wantCode    int
		wantUp      bool
	}{
		{"запись витрины доходит до сервера", "application/json", true, "same-origin", http.StatusCreated, true},
		{"без заголовка витрины — отказ", "application/json", false, "", http.StatusForbidden, false},
		{"форма вместо JSON — отказ", "text/plain", true, "", http.StatusUnsupportedMediaType, false},
		{"межсайтовая — отказ", "application/json", true, "cross-site", http.StatusForbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *http.Request
			var body string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Clone(r.Context())
				b, _ := io.ReadAll(r.Body)
				body = string(b)
				w.WriteHeader(http.StatusCreated)
			}))
			defer up.Close()
			req := httptest.NewRequest(http.MethodPost, "/app/api/finances/transactions", strings.NewReader(`{"amount":"350"}`))
			req.Header.Set("Content-Type", tc.contentType)
			if tc.appHeader {
				req.Header.Set("X-DVK-App", "1")
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			rec := httptest.NewRecorder()
			proxyTo(t, up, "tok-1").ServeHTTP(rec, req)
			if rec.Code != tc.wantCode || (got != nil) != tc.wantUp {
				t.Fatalf("код %d (want %d), дошло до сервера: %v", rec.Code, tc.wantCode, got != nil)
			}
			if !tc.wantUp {
				return
			}
			if got.Method != http.MethodPost || body != `{"amount":"350"}` || got.Header.Get("X-KB-Token") != "tok-1" {
				t.Errorf("на сервер ушло %s %q, токен %q", got.Method, body, got.Header.Get("X-KB-Token"))
			}
			if got.Header.Get("Content-Type") != "application/json" {
				t.Error("Content-Type не дошел: сервер отказал бы 415")
			}
		})
	}
}

// Путь вне /app — не дело прокси: окно отдает его своему API.
func TestAppProxy_passesOtherPathsOn(t *testing.T) {
	if isAppPath("/api/search") || isAppPath("/application") || !isAppPath("/app") || !isAppPath("/app/x") {
		t.Fatal("isAppPath путает /app с соседними путями")
	}
}

// Витрина встраивается в окно из desktop/app-ui (G4). В git там только .keep:
// без сборки (go test, CI без npm) /app/ говорит «не собрана», а не отдает 404.
func TestBuiltAppUI(t *testing.T) {
	if builtAppUI(fstest.MapFS{".keep": {}}) != nil {
		t.Error("папка без index.html принята за собранную витрину")
	}
	if builtAppUI(appUI) == nil {
		t.Error("собранная витрина не принята")
	}
}

// Отказ сервера витрина показывает человеку словами «что не так — что
// сделать» (kbserver.Describe), а не голым «502 Bad Gateway». Свои ответы
// сервера на запись (409, 400) проходят как есть: их читает форма операции.
func TestAppProxy_explainsFailures(t *testing.T) {
	answer := func(code int, body string) roundTripFunc {
		return func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
	}
	down := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })
	redirect := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://x"}}, Body: http.NoBody, Request: r}, nil
	})
	const remote, local = "https://kb.example.ru", "http://127.0.0.1:8097"
	for _, c := range []struct {
		name, url string
		rt        roundTripFunc
		code      int
		want      string // подстрока ответа
	}{
		{"502 облака", remote, answer(http.StatusBadGateway, "<html>Bad Gateway</html>"), http.StatusBadGateway, "сервер не в порядке — "},
		{"500 сервера без пути к файлу", remote, answer(http.StatusInternalServerError, "open /Users/x/ledger.jsonl: denied"), http.StatusInternalServerError, "журнал сервера"},
		{"401", remote, answer(http.StatusUnauthorized, ""), http.StatusUnauthorized, "введите токен заново"},
		{"403", remote, answer(http.StatusForbidden, ""), http.StatusForbidden, "проверьте адрес"},
		{"сервер недоступен", remote, down, http.StatusBadGateway, "нет связи с сервером — "},
		{"свой serve не запущен", local, down, http.StatusBadGateway, "kbengine serve"},
		{"перенаправление", remote, redirect, http.StatusBadGateway, "конечный адрес"},
		{"409 формы как есть", remote, answer(http.StatusConflict, "такая запись уже есть"), http.StatusConflict, "такая запись уже есть"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newAppProxy(func() (appUpstream, bool) { return appUpstream{URL: c.url, Token: "tok-1"}, true }, appUI, c.rt)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/api/finances", nil))
			body := rec.Body.String()
			if rec.Code != c.code || !strings.Contains(body, c.want) {
				t.Errorf("ответ %d %q, want %d с %q", rec.Code, body, c.code, c.want)
			}
			if strings.Contains(body, "/Users/") {
				t.Errorf("путь к файлу сервера ушел в окно: %q", body)
			}
		})
	}
}

// Находки ревью #511: что прокси НЕ переписывает и что сохраняет.
func TestAppProxy_explainsOnlyWhatItShould(t *testing.T) {
	answer := func(code int, body string, h http.Header) roundTripFunc {
		return func(r *http.Request) (*http.Response, error) {
			if h == nil {
				h = http.Header{}
			}
			return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
	}
	const remote, local = "https://kb.example.ru", "http://127.0.0.1:8097"
	write := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/app/api/finances/transactions", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(appWriteHeader, "1")
		return r
	}
	read := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/app/api/finances", nil) }
	for _, c := range []struct {
		name, url, token string // token пуст — «этот Mac» без токена
		req              func() *http.Request
		rt               roundTripFunc
		code             int
		want             []string // подстроки ответа
	}{
		// Запись читает форма операции: свой текст сервера (CSRF, токен) — как есть.
		{"запись: 403 сервера как есть", remote, "tok-1", write, answer(http.StatusForbidden, "cross-site write refused", nil), http.StatusForbidden, []string{"cross-site write refused"}},
		{"запись: 401 сервера как есть", remote, "tok-1", write, answer(http.StatusUnauthorized, "unauthorized", nil), http.StatusUnauthorized, []string{"unauthorized"}},
		// Свой serve: текст с этого же Мака и совет про терминал, а не «попробуйте позже».
		{"свой serve: 500 со своим текстом", local, "", read, answer(http.StatusInternalServerError, "open ledger.jsonl: permission denied", nil), http.StatusInternalServerError, []string{"permission denied", "терминал"}},
		{"свой serve: 401 — про --token-file", local, "", read, answer(http.StatusUnauthorized, "", nil), http.StatusUnauthorized, []string{"--token-file"}},
		// Сервер из источников на loopback (туннель) с токеном: 401 — это токен.
		{"loopback с токеном: 401 — про токен", local, "tok-1", read, answer(http.StatusUnauthorized, "", nil), http.StatusUnauthorized, []string{"введите токен заново"}},
		// 304 — ответ на проверку кэша, а не перенаправление.
		{"304 проходит", remote, "tok-1", read, answer(http.StatusNotModified, "", http.Header{"Etag": {`"x"`}}), http.StatusNotModified, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newAppProxy(func() (appUpstream, bool) { return appUpstream{URL: c.url, Token: c.token}, true }, appUI, c.rt)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, c.req())
			if rec.Code != c.code {
				t.Errorf("код %d, want %d (%q)", rec.Code, c.code, rec.Body.String())
			}
			for _, w := range c.want {
				if !strings.Contains(rec.Body.String(), w) {
					t.Errorf("ответ %q, want с %q", rec.Body.String(), w)
				}
			}
		})
	}

	// Заголовки отказа, которые значат для клиента, не теряются.
	p := newAppProxy(func() (appUpstream, bool) { return appUpstream{URL: remote, Token: "tok-1"}, true }, appUI,
		answer(http.StatusTooManyRequests, "slow down", http.Header{"Retry-After": {"30"}}))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, read())
	if rec.Header().Get("Retry-After") != "30" {
		t.Errorf("Retry-After потерян: %v", rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("отказ без Cache-Control: no-store: %v", rec.Header())
	}
}
