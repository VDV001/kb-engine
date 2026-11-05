package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

const (
	secretA = "ALPHA-ONLY-7f3c"
	secretB = "BETA-ONLY-9d21"
)

type tenantFixture struct {
	h          http.Handler
	tokA, tokB string
}

// newTenants registers two people whose catalogs carry a marker each and
// builds the multi-tenant server over them.
func newTenants(t *testing.T) tenantFixture {
	t.Helper()
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	add := func(name, marker string) string {
		t.Helper()
		uid, err := domain.NewUserID(name)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := userauth.Add(ctx, s, uid)
		if err != nil {
			t.Fatal(err)
		}
		key := userauth.CatalogKey(uid)
		cur, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		cat := `{"entries":[{"id":1,"habr_id":1,"title":"` + marker + `","url":"https://h/` + marker +
			`","category":"golang","status":"keep","author":"` + marker + `","tags":["` + strings.ToLower(marker) + `"]},` +
			`{"id":2,"title":"note ` + marker + `","type":"note","category":"golang","status":"keep","file":"notes/n.md"}]}`
		if _, err := s.Put(ctx, key, []byte(cat), cur.ETag); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, "users/"+name+"/kb/notes/n.md", []byte("# "+marker+"\n"), ""); err != nil {
			t.Fatal(err)
		}
		return tok
	}
	tokA, tokB := add("alpha", secretA), add("beta", secretB)
	var stderr bytes.Buffer
	h := tenantsHandler(ctx, s, &stderr)
	// У каждого еще и операция в журнале с той же меткой: тест изоляции по
	// всем GET-маршрутам проверяет и финансы.
	for tok, marker := range map[string]string{tokA: secretA, tokB: secretB} {
		rec := do(h, http.MethodPost, "/api/finances/transactions",
			`{"kind":"expense","amount":"418","category":"Транспорт","place":"`+marker+`","description":"`+marker+`","date":"2026-04-06"}`, tok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("fixture transaction: %d %s", rec.Code, rec.Body.String())
		}
	}
	return tenantFixture{h: h, tokA: tokA, tokB: tokB}
}

func get(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// registeredGETs reads the GET routes from their registration lines, so a
// route added later is checked without anyone listing it here.
func registeredGETs(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("../../internal/adapter/httpapi/server.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`mux\.Handle(?:Func)?\("GET (/[^"]*)"`)
	var paths []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		p := m[1]
		p = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(p, "1")
		switch {
		case p == "/kb/":
			p = "/kb/notes/n.md"
		case strings.HasSuffix(p, "/"):
			p += "x"
		}
		paths = append(paths, p)
	}
	if len(paths) < 15 {
		t.Fatalf("found %d GET routes in server.go — the pattern no longer matches the registrations", len(paths))
	}
	return append(paths, "/api/search?q="+strings.ToLower(secretA), "/api/search?q="+strings.ToLower(secretB), "/", "/api/entries/1")
}

// Главный тест изоляции: с токеном Б ни один маршрут не отдает ничего из базы
// А. Положительный контроль — сам А свою метку на тех же маршрутах видит, иначе
// «не нашли» значило бы только «метка нигде не показывается».
func TestTenantsAreIsolatedOnEveryRoute(t *testing.T) {
	f := newTenants(t)
	seenA := 0
	for _, p := range registeredGETs(t) {
		if rec := get(f.h, p, f.tokB); strings.Contains(readable(t, rec), secretA) {
			t.Errorf("%s with B's token shows A's data (status %d)", p, rec.Code)
		}
		bodyA := readable(t, get(f.h, p, f.tokA))
		if strings.Contains(bodyA, secretA) {
			seenA++
		} else if strings.HasSuffix(p, ".xlsx") {
			// Выгрузка сжата: без распаковки проверка утечки через нее прошла бы
			// впустую. Своя метка в своей книге доказывает, что текст читается.
			t.Errorf("%s with A's token does not show A's own data: the leak check would pass vacuously", p)
		}
		if strings.Contains(bodyA, secretB) {
			t.Errorf("%s with A's token shows B's data", p)
		}
	}
	if seenA < 3 {
		t.Fatalf("A's marker visible on %d routes for A — the isolation check would pass vacuously", seenA)
	}
}

func TestTenantsRefuseWithoutAValidToken(t *testing.T) {
	f := newTenants(t)
	for _, tok := range []string{"", "nope", f.tokA + "x", strings.Repeat("a", 43)} {
		rec := get(f.h, "/api/stats", tok)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", tok, rec.Code)
		}
		if strings.Contains(rec.Body.String(), secretA) || strings.Contains(rec.Body.String(), secretB) {
			t.Errorf("token %q: 401 body leaks data", tok)
		}
	}
	// Пробы здоровья и готовности ходят без токена и ничего не рассказывают:
	// за токеном под в Kubernetes перезапускался бы по кругу.
	for _, p := range []string{"/healthz", "/readyz"} {
		rec := get(f.h, p, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s without a token: %d, want 200", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), secretA) || strings.Contains(rec.Body.String(), "users/") {
			t.Errorf("%s leaks: %q", p, rec.Body.String())
		}
	}
}

func TestTenantsFlagsConflict(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"--tenants", t.TempDir(), "--catalog", "x.json"},
		{"--tenants", t.TempDir(), "--token-file", "tok"},
		{"--tenants", t.TempDir(), "--files-index", "idx.gob"},
		{"--tenants", t.TempDir(), "--files-exclude", "node_modules"},
		{"--tenants", t.TempDir(), "--s3-token-file", "tok"},
	} {
		errb.Reset()
		// Адрес, на котором прослушивание падает сразу: без отказа на флагах
		// serve завершится кодом 1, а не повиснет на настоящем порту.
		args = append(args, "--addr", "127.0.0.1:-1")
		if code := runServe(args, &out, &errb); code != 2 {
			t.Errorf("serve %v: exit %d, want 2 (a mistake in the command)", args, code)
		}
		if !strings.Contains(errb.String(), "--tenants") {
			t.Errorf("serve %v: stderr %q does not name --tenants", args, errb.String())
		}
	}
}

// Сломанная база одного человека не задерживает остальных: обработчик
// собирается вне общего замка, готовые отдаются без ожидания.
func TestOneBrokenBaseDoesNotStallOthers(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string) string {
		uid, err := domain.NewUserID(name)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := userauth.Add(ctx, s, uid)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	tokGood, tokSlow := mk("good"), mk("slow")
	gs := &gateCatalog{Store: s, key: "users/slow/kb/_data/catalog.json", gate: make(chan struct{})}
	h := tenantsHandler(ctx, gs, io.Discard)
	if rec := get(h, "/api/stats", tokGood); rec.Code != http.StatusOK {
		t.Fatalf("good: %d", rec.Code)
	}
	go get(h, "/api/stats", tokSlow) // blocks building slow's handler
	for gs.waiting.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	done := make(chan int, 1)
	go func() { done <- get(h, "/api/stats", tokGood).Code }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("good while slow builds: %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a request of another person waited for slow's handler to build")
	}
	close(gs.gate)
}

// gateCatalog holds reads of one key until gate is closed.
type gateCatalog struct {
	blob.Store
	key     string
	gate    chan struct{}
	waiting atomic.Int64
}

func (g *gateCatalog) GetIfChanged(ctx context.Context, key, etag string) (blob.Object, error) {
	if key == g.key {
		g.waiting.Add(1)
		<-g.gate
	}
	return g.Store.GetIfChanged(ctx, key, etag)
}

// readable is the body as text to search: книга xlsx — zip, и метка внутри нее
// сжата, поэтому архив распаковывается и тексты его частей склеиваются.
func readable(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	raw := rec.Body.Bytes()
	if !bytes.HasPrefix(raw, []byte("PK\x03\x04")) {
		return string(raw)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("a zip body that does not open: %v", err)
	}
	var out strings.Builder
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		part, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out.Write(part)
	}
	return out.String()
}
