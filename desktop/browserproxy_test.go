package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Модуль «Браузер»: окно ходит на kbengine browser serve этого Mac само, со
// своим токеном. Страница токена не видит и свой Cookie/Authorization не
// передает: порт на loopback достанется любому процессу, а прокси его закрывает.

func browserProxyTo(up *httptest.Server, token string) http.Handler {
	return newBrowserProxy(func() browserUpstream { return browserUpstream{URL: up.URL, Token: token} }, up.Client().Transport)
}

func TestIsBrowserPath(t *testing.T) {
	for p, want := range map[string]bool{"/browser": true, "/browser/": true, "/browser/frame": true, "/browserx": false, "/app/": false, "/": false} {
		if got := isBrowserPath(p); got != want {
			t.Errorf("isBrowserPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestBrowserProxy_addsTokenStripsPrefixDropsCredentials(t *testing.T) {
	var got *http.Request
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("X-Frame-Seq", "7")
		w.Header().Set("Content-Type", "image/jpeg")
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
		_, _ = io.WriteString(w, "jpeg")
	}))
	defer up.Close()

	req := httptest.NewRequest(http.MethodGet, "/browser/frame?after=3", nil)
	req.Header.Set("Cookie", "webview=1")
	req.Header.Set("Authorization", "Bearer from-page")
	req.Header.Set("X-KB-Token", "forged")
	rec := httptest.NewRecorder()
	browserProxyTo(up, "tok-b").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "jpeg" || rec.Header().Get("X-Frame-Seq") != "7" {
		t.Fatalf("ответ %d %q seq=%q", rec.Code, rec.Body.String(), rec.Header().Get("X-Frame-Seq"))
	}
	if got.URL.Path != "/frame" || got.URL.RawQuery != "after=3" {
		t.Errorf("на сервер ушло %s?%s, want /frame?after=3", got.URL.Path, got.URL.RawQuery)
	}
	if v := got.Header.Get("X-KB-Token"); v != "tok-b" {
		t.Errorf("X-KB-Token = %q, want токен окна, а не страницы", v)
	}
	for _, h := range []string{"Cookie", "Authorization"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("%s от webview ушел на сервер: %q", h, v)
		}
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("Set-Cookie сервера дошел до страницы")
	}
}

func TestBrowserProxy_rootRedirectsToSlash(t *testing.T) {
	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	rec := httptest.NewRecorder()
	browserProxyTo(up, "t").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/browser", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/browser/" {
		t.Fatalf("/browser: %d %q, want 301 /browser/ (относительные адреса страницы иначе уйдут в корень окна)", rec.Code, rec.Header().Get("Location"))
	}
}

func TestBrowserProxy_writeGuard(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		appHeader   string
		site        string
		want        int
	}{
		{"из страницы", "application/json", "1", "", http.StatusNoContent},
		{"не JSON", "text/plain", "1", "", http.StatusUnsupportedMediaType},
		{"без заголовка страницы", "application/json", "", "", http.StatusForbidden},
		{"межсайтовый", "application/json", "1", "cross-site", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hit := false
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit = true; w.WriteHeader(http.StatusNoContent) }))
			defer up.Close()
			req := httptest.NewRequest(http.MethodPost, "/browser/input", strings.NewReader(`{"t":"text","text":"a"}`))
			req.Header.Set("Content-Type", tc.contentType)
			if tc.appHeader != "" {
				req.Header.Set(appWriteHeader, tc.appHeader)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			rec := httptest.NewRecorder()
			browserProxyTo(up, "t").ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("код %d, want %d", rec.Code, tc.want)
			}
			if hit != (tc.want == http.StatusNoContent) {
				t.Fatalf("сервер получил запрос: %v", hit)
			}
		})
	}
}

func TestBrowserProxy_explainsWhyThereIsNoBrowser(t *testing.T) {
	// Нет токена (сервер браузера ни разу не запускали) и нет ответа (остановлен):
	// человек видит, что запустить, а не пустую вкладку.
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	cases := map[string]http.Handler{
		"нет токена":  newBrowserProxy(func() browserUpstream { return browserUpstream{Why: "нет файла токена"} }, nil),
		"не отвечает": newBrowserProxy(func() browserUpstream { return browserUpstream{URL: down.URL, Token: "t"} }, down.Client().Transport),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/browser/", nil))
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "kbengine browser serve") {
				t.Fatalf("%d %q, want 503 со словами «kbengine browser serve»", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBrowserProxy_doesNotFollowRedirectWithToken(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("токен ушел по чужому Location: %q", r.Header.Get("X-KB-Token"))
	}))
	defer other.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer up.Close()
	rec := httptest.NewRecorder()
	browserProxyTo(up, "t").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/browser/", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("перенаправление выполнено: %d", rec.Code)
	}
}
