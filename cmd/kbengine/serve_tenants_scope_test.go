package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

type route struct{ method, path string }

// registeredWrites reads the non-GET routes from their registration lines, so a
// write route added later is checked without anyone listing it here.
func registeredWrites(t *testing.T) []route {
	t.Helper()
	var out []route
	re := regexp.MustCompile(`mux\.Handle(?:Func)?\("(POST|PUT|PATCH|DELETE) (/[^"]*)"`)
	for _, file := range []string{"../../internal/adapter/httpapi/server.go", "../../internal/adapter/httpapi/mail.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			out = append(out, route{m[1], regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(m[2], "1")})
		}
	}
	if len(out) < 10 {
		t.Fatalf("found %d write routes — the pattern no longer matches the registrations", len(out))
	}
	return out
}

// readOnlyFixture is two people where alpha has the read scope.
func readOnlyFixture(t *testing.T) tenantFixture {
	t.Helper()
	f := newTenants(t)
	setAlphaRead(t, f)
	return f
}

func setAlphaRead(t *testing.T, f tenantFixture) {
	t.Helper()
	alpha, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := userauth.SetScope(context.Background(), f.store, alpha, domain.ScopeRead); err != nil {
		t.Fatal(err)
	}
	f.h.(*tenants).scopes = userauth.NewScopes(context.Background(), f.store, nil)
}

// Токен с областью read читает все, что читал, и не пишет НИКУДА: ни по одному
// маршруту записи, даже с пустым телом (403 раньше разбора тела). Сосед с полным
// токеном пишет как прежде. Положительные контроли: чтение у A работает, запись
// у B проходит, иначе «403 везде» значило бы «сломано».
func TestReadScopeReadsEverythingAndWritesNothing(t *testing.T) {
	f := readOnlyFixture(t)
	seenA := 0
	for _, p := range registeredGETs(t) {
		rec := get(f.h, p, f.tokA)
		if rec.Code == http.StatusForbidden {
			t.Errorf("GET %s with the read token: 403", p)
		}
		if strings.Contains(readable(t, rec), secretA) {
			seenA++
		}
	}
	if seenA < 3 {
		t.Fatalf("A's data visible on %d routes: the read check would pass vacuously", seenA)
	}
	for _, r := range registeredWrites(t) {
		if rec := do(f.h, r.method, r.path, `{}`, f.tokA); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with the read token: %d, want 403", r.method, r.path, rec.Code)
		}
	}
	if rec := do(f.h, http.MethodPatch, "/api/entries/1", `{"title":"`+editedByB+`"}`, f.tokB); rec.Code != http.StatusOK {
		t.Fatalf("the neighbour's write: %d", rec.Code)
	}
	if body := readable(t, get(f.h, "/api/entries", f.tokA)); !strings.Contains(body, secretA) || strings.Contains(body, editedByB) {
		t.Errorf("A's entries after the attempts: %s", body)
	}
}

// Телефон и компьютер человека с областью read тоже только читают.
func TestReadScopeAppliesToThePhoneAndTheMailbox(t *testing.T) {
	f := newTenants(t)
	frozen(f)
	_, phone := addPhone(t, f, f.tokA) // телефон заведен до ограничения
	setAlphaRead(t, f)
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusOK {
		t.Errorf("phone poll: %d, want 200", rec.Code)
	}
	for _, c := range []struct{ method, path, body, token string }{
		{http.MethodPost, "/api/mail/c-1", `{"sealed":"AAAA"}`, phone},
		{http.MethodPost, "/api/mail/c-1", `{"sealed":"AAAA"}`, f.tokA},
		{http.MethodPost, "/api/mail/devices", ``, f.tokA},
		{http.MethodDelete, "/api/mail/files/00000000000000000000000000000000", ``, phone},
	} {
		if rec := do(f.h, c.method, c.path, c.body, c.token); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if rec := putFile(f, []byte("x"), phone); rec.Code != http.StatusForbidden {
		t.Errorf("file put by the read-only phone: %d, want 403", rec.Code)
	}
}

type scopesDown struct{ blob.Store }

func (s scopesDown) GetIfChanged(ctx context.Context, key, etag string) (blob.Object, error) {
	if key == userauth.ScopesKey {
		return blob.Object{}, errors.New("storage is down")
	}
	return s.Store.GetIfChanged(ctx, key, etag)
}

func (s scopesDown) Get(ctx context.Context, key string) (blob.Object, error) {
	if key == userauth.ScopesKey {
		return blob.Object{}, errors.New("storage is down")
	}
	return s.Store.Get(ctx, key)
}

// Закрыто, а не открыто: если файл областей не прочитать, пишет никто, читают все.
func TestScopesUnreadableMeansNobodyWrites(t *testing.T) {
	f := newTenants(t)
	f.h.(*tenants).scopes = userauth.NewScopes(context.Background(), scopesDown{f.store}, nil)
	if rec := do(f.h, http.MethodPatch, "/api/entries/1", `{"title":"x"}`, f.tokB); rec.Code != http.StatusForbidden && rec.Code != http.StatusServiceUnavailable {
		t.Errorf("write with an unreadable scopes file: %d, want a refusal", rec.Code)
	}
	if rec := get(f.h, "/api/entries", f.tokB); rec.Code != http.StatusOK {
		t.Errorf("read with an unreadable scopes file: %d, want 200", rec.Code)
	}
}
