package main

import (
	"html"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Модуль «Браузер»: страница-экран под /browser/ в окне и прокси ее запросов
// на kbengine browser serve этого Mac с токеном из файла.
//
// Только из обработчика Wails, как и «Финансы»: страница токена не видит,
// а порт на loopback достался бы любому процессу Мака без токена.

// browserUpstream is where the browser server listens and the token it wants;
// Why — почему его нет, словами для экрана.
type browserUpstream struct{ URL, Token, Why string }

// defaultBrowserAddr — где слушает `kbengine browser serve` по умолчанию.
//
// ponytail: порт зашит. Потолок — второй экземпляр или занятый порт; путь —
// поле Server у модуля (как у «Финансов») и флаг --addr при запуске.
const defaultBrowserAddr = "http://127.0.0.1:8790"

const browserHint = "Браузер не запущен: выполните в терминале kbengine browser serve — он создаст токен и откроет браузер. Потом обновите вкладку."

// isBrowserPath reports whether the path belongs to the Browser module.
func isBrowserPath(p string) bool { return p == "/browser" || strings.HasPrefix(p, "/browser/") }

// newBrowserProxy serves /browser: the viewer page and its frame and input
// calls, forwarded to the server with the window's token.
func newBrowserProxy(upstream func() browserUpstream, rt http.RoundTripper) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/browser" {
			// Страница ходит по относительным адресам: без слэша они искались бы в корне окна.
			http.Redirect(w, r, "/browser/", http.StatusMovedPermanently)
			return
		}
		up := upstream()
		base, err := url.Parse(up.URL)
		if up.URL == "" || up.Token == "" || err != nil {
			writeBrowserPage(w, or(up.Why, browserHint))
			return
		}
		if code, msg := appWriteRefused(r); code != 0 {
			http.Error(w, msg, code)
			return
		}
		(&httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(base)
				pr.Out.URL.Path = strings.TrimPrefix(r.URL.Path, "/browser")
				pr.Out.URL.RawPath = ""
				// Список запрещенного короткий, потому что заголовков у страницы мало:
				// токен и вход — только окна.
				for _, h := range []string{"Cookie", "Authorization", "X-KB-Token"} {
					pr.Out.Header.Del(h)
				}
				pr.Out.Header.Set("X-KB-Token", up.Token)
			},
			Transport:      rt,
			FlushInterval:  -1,
			ModifyResponse: func(resp *http.Response) error { resp.Header.Del("Set-Cookie"); return nil },
			ErrorHandler:   func(w http.ResponseWriter, _ *http.Request, _ error) { writeBrowserPage(w, browserHint) },
		}).ServeHTTP(w, r)
	})
}

func writeBrowserPage(w http.ResponseWriter, why string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Браузер</title>` +
		`<body style="font:14px ui-monospace,Menlo,monospace;background:#0b0b0c;color:#c4c4c6;padding:40px">` +
		`<p>` + html.EscapeString(why) + `</p></body>`))
}

// browserUpstream returns where the module takes its page: свой kbengine
// browser serve на этом Mac; токен — из файла, который он сам создал.
func (f *folderSettings) browserUpstream() browserUpstream {
	if !f.snapshot().cfg.ModuleOn(desktopconfig.ModuleBrowser) {
		return browserUpstream{Why: "Модуль «Браузер» выключен: включите его в «Настройки → Окно → Модули»."}
	}
	raw, err := os.ReadFile(filepath.Join(f.home, ".kb-tokens", "browser"))
	if err != nil {
		return browserUpstream{} // нет файла — сервер ни разу не запускали
	}
	return browserUpstream{URL: defaultBrowserAddr, Token: strings.TrimSpace(string(raw))}
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
