package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Модуль «Браузер»: окно само отдает страницу-экран и ходит на сокет
// kbengine browser serve со своим токеном, пропуская только /frame и /input.
// Страница от сервера выполнилась бы в источнике окна, а сервер на порту мог
// занять чужой процесс (security-ревью #526, F1).

// browserSocket starts a server on a unix socket the way kbengine browser serve does.
func browserSocket(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kbd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(h)
	_ = ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	t.Cleanup(ts.Close)
	return sock
}

func browserProxyAt(sock, token string) http.Handler {
	return newBrowserProxy(func() browserUpstream { return browserUpstream{Socket: sock, Token: token} })
}

func TestIsBrowserPath(t *testing.T) {
	for p, want := range map[string]bool{"/browser": true, "/browser/": true, "/browser/frame": true, "/browserx": false, "/app/": false, "/": false} {
		if got := isBrowserPath(p); got != want {
			t.Errorf("isBrowserPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestBrowserProxy_servesThePageItself(t *testing.T) {
	hit := false
	sock := browserSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, "<script>evil()</script>")
	}))
	rec := httptest.NewRecorder()
	browserProxyAt(sock, "t").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/browser/", nil))
	if rec.Code != http.StatusOK || hit || strings.Contains(rec.Body.String(), "evil") {
		t.Fatalf("страница: %d, сервер опрошен: %v, тело %.60q — страницу отдает окно, не сервер", rec.Code, hit, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "frame?after=") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("это не страница-экран: %q %.60q", rec.Header().Get("Content-Type"), rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'self'", "img-src 'self' blob: data:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q без %q", csp, want)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("нет nosniff")
	}
	// Страница открывается и без сервера: причину она скажет сама, из ответа /frame.
	off := httptest.NewRecorder()
	newBrowserProxy(func() browserUpstream { return browserUpstream{} }).ServeHTTP(off, httptest.NewRequest(http.MethodGet, "/browser/", nil))
	if off.Code != http.StatusOK {
		t.Fatalf("без сервера страница: %d, want 200", off.Code)
	}
}

func TestBrowserProxy_rootRedirectsToSlash(t *testing.T) {
	rec := httptest.NewRecorder()
	browserProxyAt("/nonexistent", "t").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/browser", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/browser/" {
		t.Fatalf("/browser: %d %q, want 301 /browser/ (относительные адреса страницы иначе уйдут в корень окна)", rec.Code, rec.Header().Get("Location"))
	}
}

func TestBrowserProxy_framePassesOnlyTheFrame(t *testing.T) {
	var got *http.Request
	sock := browserSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("Content-Type", "text/html") // сервер сказал «страница», окно не верит
		w.Header().Set("X-Frame-Seq", "7")
		w.Header().Set("X-Frame-Epoch", "e1")
		w.Header().Set("X-Frame-URL", "https%3A%2F%2Fexample.com")
		w.Header().Set("X-Secret", "no")
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
		_, _ = io.WriteString(w, "jpeg")
	}))
	req := httptest.NewRequest(http.MethodGet, "/browser/frame?after=3", nil)
	req.Header.Set("Cookie", "webview=1")
	req.Header.Set("Authorization", "Bearer from-page")
	req.Header.Set("X-KB-Token", "forged")
	rec := httptest.NewRecorder()
	browserProxyAt(sock, "tok-b").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "jpeg" {
		t.Fatalf("ответ %d %q", rec.Code, rec.Body.String())
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
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg принудительно", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("нет nosniff у кадра")
	}
	for h, want := range map[string]string{"X-Frame-Seq": "7", "X-Frame-Epoch": "e1", "X-Frame-URL": "https%3A%2F%2Fexample.com"} {
		if rec.Header().Get(h) != want {
			t.Errorf("%s = %q, want %q", h, rec.Header().Get(h), want)
		}
	}
	for _, h := range []string{"Set-Cookie", "X-Secret"} {
		if rec.Header().Get(h) != "" {
			t.Errorf("%s сервера дошел до страницы", h)
		}
	}
}

func TestBrowserProxy_inputGuardAndNoBodyBack(t *testing.T) {
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
			sock := browserSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hit = true
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "<h1>тело, которое страница не увидит</h1>")
			}))
			req := httptest.NewRequest(http.MethodPost, "/browser/input", strings.NewReader(`{"t":"text","text":"a"}`))
			req.Header.Set("Content-Type", tc.contentType)
			if tc.appHeader != "" {
				req.Header.Set(appWriteHeader, tc.appHeader)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			rec := httptest.NewRecorder()
			browserProxyAt(sock, "t").ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("код %d, want %d", rec.Code, tc.want)
			}
			if hit != (tc.want == http.StatusNoContent) {
				t.Fatalf("сервер получил запрос: %v", hit)
			}
			if rec.Body.Len() != 0 && tc.want == http.StatusNoContent {
				t.Fatalf("тело ответа ввода дошло до страницы: %q", rec.Body.String())
			}
		})
	}
}

func TestBrowserProxy_allowlist(t *testing.T) {
	hit := false
	sock := browserSocket(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	h := browserProxyAt(sock, "t")
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/browser/other", http.StatusNotFound},
		{"GET", "/browser/index.html", http.StatusNotFound},
		{"GET", "/browser/frame/x", http.StatusNotFound},
		{"GET", "/browser/../app/api/finances", http.StatusNotFound},
		{"POST", "/browser/frame", http.StatusMethodNotAllowed},
		{"GET", "/browser/input", http.StatusMethodNotAllowed},
		{"DELETE", "/browser/frame", http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(appWriteHeader, "1")
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
	if hit {
		t.Fatal("лишний путь дошел до сервера")
	}
}

func TestBrowserProxy_explainsWhyThereIsNoBrowserInPlainText(t *testing.T) {
	// Нет токена и нет сокета: человек видит строкой, что запустить, а не HTML-страницу.
	gone := filepath.Join(os.TempDir(), "kbd-no-such.sock")
	cases := map[string]http.Handler{
		"нет токена":  newBrowserProxy(func() browserUpstream { return browserUpstream{} }),
		"не отвечает": browserProxyAt(gone, "t"),
		"причина слова": newBrowserProxy(func() browserUpstream {
			return browserUpstream{Why: "Модуль «Браузер» выключен: kbengine browser serve"}
		}),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/browser/frame?after=0", "/browser/input"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if strings.HasSuffix(path, "input") {
					req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"t":"text","text":"a"}`))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set(appWriteHeader, "1")
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				body := rec.Body.String()
				if rec.Code < 502 || rec.Code > 503 || strings.Contains(body, "<") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || !strings.Contains(body, "kbengine browser serve") {
					t.Fatalf("%s: %d %q %q, want 502/503 text/plain со словами «kbengine browser serve»", path, rec.Code, rec.Header().Get("Content-Type"), body)
				}
			}
		})
	}
}

func TestBrowserProxy_redirectFromServerIsAnError(t *testing.T) {
	sock := browserSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://other.example/", http.StatusFound)
	}))
	rec := httptest.NewRecorder()
	browserProxyAt(sock, "t").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/browser/frame?after=0", nil))
	if rec.Code != http.StatusBadGateway || rec.Header().Get("Location") != "" {
		t.Fatalf("перенаправление сервера: %d Location=%q, want 502 без Location", rec.Code, rec.Header().Get("Location"))
	}
}

func TestReadBrowserToken(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := strings.Repeat("a", 43)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(write("real", good+"\n", 0o600), link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, want string
		ok               bool
	}{
		{"хороший", write("good", good+"\n", 0o600), good, true},
		{"нет файла", filepath.Join(dir, "none"), "", false},
		{"права 0644", write("open", good, 0o644), "", false},
		{"права 0640", write("grp", good, 0o640), "", false},
		{"короткий", write("short", "abc", 0o600), "", false},
		{"не ASCII", write("cyr", strings.Repeat("я", 40), 0o600), "", false},
		{"символьная ссылка", link, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readBrowserToken(tc.path)
			if (err == nil) != tc.ok || got != tc.want {
				t.Fatalf("readBrowserToken = %q, %v; want %q ok=%v", got, err, tc.want, tc.ok)
			}
		})
	}
}
