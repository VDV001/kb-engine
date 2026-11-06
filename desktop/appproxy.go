package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
)

// Модуль «Финансы» (#435, G2): витрина движка под /app/ в окне и прокси ее
// запросов на kb-сервер с токеном из Связки ключей.
//
// Только из обработчика Wails, никогда с сетевого порта: loopback-порт отдал
// бы данные с токеном любому процессу Мака. Поэтому в режиме -browser /app нет.

// appUpstream is the server the Finances module talks to.
type appUpstream struct{ URL, Token string }

// appProxied are the витрина's own paths that live on the server.
var appProxied = []string{"/app/api/", "/app/kb/", "/app/media/"}

// appForwarded are the request headers that reach the server. Список
// разрешенных, а не запрещенных: Cookie, Authorization и X-KB-Token страницы
// не уходят никогда, и новый заголовок webview не утечет сам собой.
var appForwarded = []string{"Accept", "Accept-Language", "If-None-Match", "If-Modified-Since", "Range", "Content-Type"}

const noServerText = "Нет kb-сервера: подключите его в «Источниках» и включите — финансы живут на сервере."

// errAppRedirect is a redirect the proxy refused to follow: токен не уходит по
// чужому Location.
var errAppRedirect = errors.New("сервер ответил перенаправлением")

// isAppPath reports whether the path belongs to the Finances module.
func isAppPath(p string) bool { return p == "/app" || strings.HasPrefix(p, "/app/") }

// newAppProxy serves /app: the built витрина from ui and its API on the
// server. ui nil — витрина еще не собрана в окно (G4). rt nil — обычный
// транспорт.
func newAppProxy(upstream func() (appUpstream, bool), ui fs.FS, rt http.RoundTripper) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app" {
			// Код витрины грузится по ./assets/ от страницы: без слэша он
			// искался бы в корне окна.
			target := "/app/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		up, ok := upstream()
		proxied := isProxied(r.URL.Path)
		switch {
		case !ok && proxied:
			writeAppError(w, http.StatusServiceUnavailable, noServerText)
		case !ok:
			writeNoServerPage(w)
		case proxied:
			proxyToServer(w, r, up, rt)
		default:
			serveAppUI(w, r, ui)
		}
	})
}

func isProxied(p string) bool {
	for _, prefix := range appProxied {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// proxyToServer sends one витрина request to the server with the token.
func proxyToServer(w http.ResponseWriter, r *http.Request, up appUpstream, rt http.RoundTripper) {
	// ponytail: только чтение до G3 — запись через прокси откроется вместе с
	// решением по защите от CSRF (#435, G3).
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAppError(w, http.StatusMethodNotAllowed, "запись из окна — следующим шагом (G3)")
		return
	}
	// Токен — только туда, куда его пускают источники: https или http этого Мака.
	if err := desktopconfig.CheckServerURL(up.URL); err != nil {
		writeAppError(w, http.StatusBadGateway, err.Error())
		return
	}
	base, err := url.Parse(strings.TrimSpace(up.URL))
	if err != nil {
		writeAppError(w, http.StatusBadGateway, err.Error())
		return
	}
	rp := &httputil.ReverseProxy{
		Transport: rt,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/app")
			pr.Out.URL.RawPath = ""
			pr.SetURL(base)
			pr.Out.Header = http.Header{}
			for _, h := range appForwarded {
				if v := pr.In.Header.Values(h); len(v) > 0 {
					pr.Out.Header[h] = v
				}
			}
			pr.Out.Header.Set(kbserver.TokenHeader, up.Token)
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errAppRedirect
			}
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeAppError(w, http.StatusBadGateway, kbserver.Describe(err))
		},
	}
	rp.ServeHTTP(w, r)
}

// builtAppUI returns the embedded витрина, nil when the window was built without
// it (go test, CI без npm): /app/ тогда говорит «не собрана», а не 404.
func builtAppUI(fsys fs.FS) fs.FS {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil
	}
	return fsys
}

// serveAppUI serves the built витрина; незнакомый путь — index.html, маршруты
// витрины живут в ней самой.
func serveAppUI(w http.ResponseWriter, r *http.Request, ui fs.FS) {
	if ui == nil {
		http.Error(w, "Витрина финансов не собрана в окно: соберите его через `just desktop`.", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/app/")
	if st, err := fs.Stat(ui, name); name == "" || err != nil || st.IsDir() {
		name = "index.html"
	}
	http.ServeFileFS(w, r, ui, name)
}

func writeAppError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeNoServerPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Финансы</title>` +
		`<body style="font:14px ui-monospace,Menlo,monospace;background:#0b0b0c;color:#c4c4c6;padding:40px">` +
		`<p>` + noServerText + `</p></body>`))
}

// appUpstream returns the first enabled server source and its token.
// ponytail: первый включенный сервер; выбор, когда их несколько, — потом (#435).
func (f *folderSettings) appUpstream() (appUpstream, bool) {
	for _, s := range f.snapshot().cfg.Sources {
		if s.Kind == desktopconfig.KindServer && s.Enabled {
			return appUpstream{URL: s.URL, Token: f.token(s.ID)}, true
		}
	}
	return appUpstream{}, false
}

// withAppProxy puts the Finances module in front of the window's API.
func withAppProxy(api http.Handler, proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAppPath(r.URL.Path) {
			proxy.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}
