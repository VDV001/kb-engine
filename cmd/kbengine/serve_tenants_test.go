package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

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
		if rec := get(f.h, p, f.tokB); strings.Contains(rec.Body.String(), secretA) {
			t.Errorf("%s with B's token shows A's data (status %d)", p, rec.Code)
		}
		if rec := get(f.h, p, f.tokA); strings.Contains(rec.Body.String(), secretA) {
			seenA++
		}
		if rec := get(f.h, p, f.tokA); strings.Contains(rec.Body.String(), secretB) {
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
	// Проба здоровья ходит без токена и ничего не рассказывает.
	if rec := get(f.h, "/healthz", ""); rec.Code != http.StatusOK {
		t.Errorf("/healthz without a token: %d, want 200", rec.Code)
	}
}

func TestTenantsFlagsConflict(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"--tenants", t.TempDir(), "--catalog", "x.json"},
		{"--tenants", t.TempDir(), "--token-file", "tok"},
	} {
		errb.Reset()
		if code := runServe(args, &out, &errb); code == 0 {
			t.Errorf("serve %v: exit 0, want a refusal", args)
		}
		if !strings.Contains(errb.String(), "--tenants") {
			t.Errorf("serve %v: stderr %q does not name --tenants", args, errb.String())
		}
	}
}
