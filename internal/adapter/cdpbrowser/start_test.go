package cdpbrowser

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Вместо белого экрана браузер открывается на своей странице: мозг, планеты базы и
// строка «адрес или запрос». Страницу отдает сам сервер (data:-адрес), без сети.

func TestStartURLCarriesThePage(t *testing.T) {
	u := StartURL()
	const prefix = "data:text/html;charset=utf-8;base64,"
	if !strings.HasPrefix(u, prefix) {
		t.Fatalf("StartURL = %.60s…, want data:-адрес", u)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(u, prefix))
	if err != nil || !strings.Contains(string(raw), "<canvas") || !strings.Contains(string(raw), `id="route"`) {
		t.Fatalf("в адресе не страница: %v", err)
	}
}

// Страница работает без сети: ничего внешнего она не грузит. Это граница доверия:
// страница живет в браузере с токенами и cookie человека.
func TestStartPageLoadsNothingExternal(t *testing.T) {
	for _, bad := range []string{`src="http`, `href="http`, "url(http", "@import", "fetch(", "XMLHttpRequest", "WebSocket", "<script src", "<link", "<iframe", "importScripts"} {
		if strings.Contains(startHTML, bad) {
			t.Errorf("страница грузит внешнее: найдено %q", bad)
		}
	}
}

func TestDisplayURL(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"стартовая страница", StartURL(), "dvk://start"},
		{"обычный адрес", "https://example.com/a?b=1", "https://example.com/a?b=1"},
		{"пусто", "", ""},
		{"чужой data: не выдается за стартовую", "data:text/html;charset=utf-8;base64,AAAA", "data:text/html;charset=utf-8;base64,AAAA"},
		{"about:blank", "about:blank", "about:blank"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayURL(tc.in); got != tc.want {
				t.Fatalf("displayURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// В строке адреса вкладки стоит dvk://start, а не длинный data:-адрес.
func TestFrameShowsStartAddress(t *testing.T) {
	c := newChrome(nil, nopCloser{})
	c.mu.Lock()
	c.url = StartURL()
	c.publishLocked([]byte("x"))
	got := c.frame.URL
	c.mu.Unlock()
	if got != "dvk://start" {
		t.Fatalf("Frame.URL = %.40q, want dvk://start", got)
	}
}

type nopCloser struct{}

func (nopCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopCloser) Close() error                { return nil }

// Страница создается сразу со стартовым адресом: пустая about:blank, загруженная
// позже, перекрывала бы переход (Brave), и вкладка оставалась белой.
func TestAttachCreatesPageWithStartURL(t *testing.T) {
	c, f := startFakeCDP(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	created := append([]string(nil), f.created...)
	f.mu.Unlock()
	if len(created) != 1 || created[0] != StartURL() {
		t.Fatalf("createTarget url = %v, want стартовая страница", created)
	}
	if navs := f.navigations(); len(navs) != 0 {
		t.Fatalf("Page.navigate = %d раз, want 0: страница уже открыта", len(navs))
	}
}

// route решает, что делать с введенным: открыть адрес, искать в сети или отказать.
// Он отдельным блоком без DOM, чтобы проверить его в node.
func TestStartRoute(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: логика строки не проверена")
	}
	m := regexp.MustCompile(`(?s)<script id="route">(.*?)</script>`).FindStringSubmatch(startHTML)
	if m == nil {
		t.Fatal(`нет блока <script id="route">`)
	}
	script := m[1] + `
const cases = JSON.parse(process.argv[2]);
const bad = [];
for (const [input, kind, url] of cases) {
  const r = route(input);
  if (r.kind !== kind || (url !== "" && r.url !== url)) bad.push(JSON.stringify([input, r, kind, url]));
}
if (bad.length) { console.error(bad.join("\n")); process.exit(1); }`
	file := t.TempDir() + "/route.js"
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	const cases = `[
	["", "none", ""],
	["   ", "none", ""],
	["example.com", "open", "https://example.com/"],
	["https://example.com/a?b=1", "open", "https://example.com/a?b=1"],
	["http://example.com", "open", "http://example.com/"],
	["localhost:3000", "open", "http://localhost:3000/"],
	["127.0.0.1:8080/x", "open", "http://127.0.0.1:8080/x"],
	["habr.com/ru/articles", "open", "https://habr.com/ru/articles"],
	["яндекс.рф", "open", "https://xn--d1acpjx3f.xn--p1ai/"],
	["javascript:alert(1)", "refused", ""],
	["JavaScript:alert(1)", "refused", ""],
	["data:text/html,<b>x</b>", "refused", ""],
	["file:///etc/passwd", "refused", ""],
	["chrome://settings", "refused", ""],
	["about:blank", "refused", ""],
	["view-source:https://example.com", "refused", ""],
	["blob:https://example.com/1", "refused", ""],
	["привет мир", "search", "https://www.google.com/search?q=%D0%BF%D1%80%D0%B8%D0%B2%D0%B5%D1%82%20%D0%BC%D0%B8%D1%80"],
	["как дела", "search", ""],
	["one", "search", "https://www.google.com/search?q=one"],
	["a b.c", "search", ""],
	["что такое kb.engine", "search", ""]
]`
	out, err := exec.Command(node, file, cases).CombinedOutput()
	if err != nil {
		t.Fatalf("route: %v\n%s", err, out)
	}
}
