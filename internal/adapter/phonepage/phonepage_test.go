package phonepage_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/phonepage"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// Страница телефона — статика без данных: отдается без токена, но под строгими
// заголовками: ни скриптов со стороны и inline, ни чужого фрейма, ни адреса
// (с ключом во фрагменте) в Referer (#622, этап 4).
func TestPageServesWithStrictHeaders(t *testing.T) {
	h := phonepage.Handler()
	rec := get(h, "/m/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Body.String(), "crypto.js") {
		t.Fatalf("/m/: %d %s %.80s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows inline or eval: %q", csp)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q", got)
	}
	if js := get(h, "/m/crypto.js"); js.Code != http.StatusOK || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Errorf("crypto.js: %d %s", js.Code, js.Header().Get("Content-Type"))
	}
	if m := get(h, "/m"); m.Code != http.StatusMovedPermanently && m.Code != http.StatusPermanentRedirect && m.Code != http.StatusFound {
		t.Errorf("/m without slash: %d, want a redirect to /m/", m.Code)
	}
	for _, p := range []string{"/m/nope.js", "/m/static/", "/m/../go.mod"} {
		if r := get(h, p); r.Code == http.StatusOK {
			t.Errorf("%s: 200, want refused", p)
		}
	}
}
