package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Модуль «Браузер»: страницу-экран под /browser/ отдает само окно, а запросы
// /frame и /input идут на unix-сокет kbengine browser serve с токеном из файла.
//
// Страница от сервера не берется никогда: HTML от того, кто слушает сокет,
// выполнился бы в источнике окна и дотянулся до привязок Go (security-ревью
// #526, F1). Сокет в папке 0700 — вместо порта на loopback, который занимает
// любой процесс Мака. Все, кроме /frame и /input, окно отвечает 404 само.

// browserUpstream is where the browser server listens and the token it wants;
// Why — почему его нет, словами для экрана (пусто — не запущен).
type browserUpstream struct{ Socket, Token, Why string }

const browserHint = "Браузер не запущен. Выполните в терминале kbengine browser serve (он создаст токен и запустит браузер), потом обновите вкладку."

var errBrowserRedirect = errors.New("сервер браузера ответил перенаправлением")

// isBrowserPath reports whether the path belongs to the Browser module.
func isBrowserPath(p string) bool { return p == "/browser" || strings.HasPrefix(p, "/browser/") }

// browserCSP — страница ходит только к себе, картинки — из кадров (blob) и ничего снаружи.
const browserCSP = "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' blob: data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"

// browserFrameHeaders are the only response headers of /frame that reach the page.
var browserFrameHeaders = []string{"X-Frame-Seq", "X-Frame-Epoch", "X-Frame-URL", "Cache-Control"}

// newBrowserProxy serves /browser: the viewer page itself and the frame and
// input calls forwarded to the server with the window's token.
func newBrowserProxy(upstream func() browserUpstream) http.Handler {
	page := cdpbrowser.Viewer(cdpbrowser.DefaultWidth, cdpbrowser.DefaultHeight)
	t := &socketTransport{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/browser" {
			// Страница ходит по относительным адресам: без слэша они искались бы в корне окна.
			http.Redirect(w, r, "/browser/", http.StatusMovedPermanently)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/browser/")
		switch {
		case name == "":
			serveBrowserPage(w, r, page)
		case name == "frame" && r.Method == http.MethodGet, name == "input" && r.Method == http.MethodPost:
			forwardBrowser(w, r, upstream(), name, t)
		case name == "frame" || name == "input":
			w.Header().Set("Allow", map[string]string{"frame": http.MethodGet, "input": http.MethodPost}[name])
			plain(w, http.StatusMethodNotAllowed, "метод не разрешен")
		default:
			plain(w, http.StatusNotFound, "нет такого пути")
		}
	})
}

func serveBrowserPage(w http.ResponseWriter, r *http.Request, page string) {
	if r.Method != http.MethodGet {
		plain(w, http.StatusMethodNotAllowed, "метод не разрешен")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", browserCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, page)
}

func forwardBrowser(w http.ResponseWriter, r *http.Request, up browserUpstream, name string, t *socketTransport) {
	if name == "input" {
		if code, msg := appWriteRefused(r); code != 0 {
			plain(w, code, msg)
			return
		}
	}
	if up.Socket == "" || up.Token == "" {
		plain(w, http.StatusServiceUnavailable, or(up.Why, browserHint))
		return
	}
	(&httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: "browser"})
			pr.Out.URL.Path, pr.Out.URL.RawPath = "/"+name, ""
			// Токен и вход — только окна: заголовки страницы не уходят на сервер.
			for _, h := range []string{"Cookie", "Authorization", "X-KB-Token"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("X-KB-Token", up.Token)
		},
		Transport:      t.to(up.Socket),
		FlushInterval:  -1,
		ModifyResponse: func(resp *http.Response) error { return shapeBrowserResponse(resp, name) },
		ErrorHandler:   func(w http.ResponseWriter, _ *http.Request, _ error) { plain(w, http.StatusBadGateway, browserHint) },
	}).ServeHTTP(w, r)
}

// shapeBrowserResponse lets through only what the page needs: кадр как image/jpeg,
// у ввода без тела, остальное текстом. Что сервер сказал о типе, окно не берет на веру.
func shapeBrowserResponse(resp *http.Response, name string) error {
	code := resp.StatusCode
	if code >= 300 && code < 400 {
		return errBrowserRedirect // токен не уходит по чужому Location
	}
	keep := http.Header{"X-Content-Type-Options": {"nosniff"}}
	switch {
	case name == "frame" && code == http.StatusOK:
		copyHeaders(keep, resp.Header, browserFrameHeaders)
		keep.Set("Content-Type", "image/jpeg")
	case code == http.StatusNoContent || (name == "input" && code/100 == 2):
		if name == "frame" {
			copyHeaders(keep, resp.Header, browserFrameHeaders) // эпоха нужна и пустому ответу
		}
		resp.StatusCode, resp.Status = http.StatusNoContent, "204 No Content"
		_ = resp.Body.Close()
		resp.Body, resp.ContentLength = http.NoBody, 0
	default:
		keep.Set("Content-Type", "text/plain; charset=utf-8")
	}
	resp.Header = keep
	return nil
}

func copyHeaders(dst, src http.Header, names []string) {
	for _, h := range names {
		if v := src.Values(h); len(v) > 0 {
			dst[http.CanonicalHeaderKey(h)] = v
		}
	}
}

func plain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg)
}

// socketTransport dials the browser server's unix socket; одно соединение
// переиспользуется между кадрами.
type socketTransport struct {
	mu   sync.Mutex
	sock string
	tr   *http.Transport
}

func (s *socketTransport) to(sock string) *http.Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tr == nil || s.sock != sock {
		if s.tr != nil {
			s.tr.CloseIdleConnections()
		}
		s.sock = sock
		s.tr = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}
	}
	return s.tr
}

// browserUpstream returns where the module takes its pictures: сокет и токен,
// которые создал `kbengine browser serve` в ~/.kb-tokens.
func (f *folderSettings) browserUpstream() browserUpstream {
	if !f.snapshot().cfg.ModuleOn(desktopconfig.ModuleBrowser) {
		return browserUpstream{Why: "Модуль «Браузер» выключен: включите его в «Настройки → Окно → Модули»."}
	}
	dir := filepath.Join(f.home, ".kb-tokens")
	token, err := readBrowserToken(filepath.Join(dir, "browser"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return browserUpstream{} // сервер ни разу не запускали
	case err != nil:
		return browserUpstream{Why: "Файл токена браузера не подходит (" + err.Error() + "): удалите ~/.kb-tokens/browser и выполните kbengine browser serve."}
	}
	return browserUpstream{Socket: filepath.Join(dir, "browser.sock"), Token: token}
}

const (
	minBrowserToken = 32
	maxBrowserToken = 4096
)

var errBrowserTokenFile = errors.New("не обычный файл с правами 0600 и токеном из печатных ASCII длиной 32-4096")

// readBrowserToken reads the token the server created: те же проверки, что у
// kbengine (обычный файл, не ссылка, права 0600, длина и состав), потому что
// окно отдает этот токен серверу, а подменить файл может тот, у кого есть запись.
func readBrowserToken(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return "", errBrowserTokenFile
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxBrowserToken+2))
	if err != nil {
		return "", err
	}
	tok := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(tok) < minBrowserToken || len(tok) > maxBrowserToken {
		return "", errBrowserTokenFile
	}
	for i := range len(tok) {
		if tok[i] < 0x21 || tok[i] > 0x7e {
			return "", errBrowserTokenFile
		}
	}
	return tok, nil
}

// withBrowserProxy puts the Browser module in front of the window's handler.
func withBrowserProxy(next http.Handler, proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBrowserPath(r.URL.Path) {
			proxy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
