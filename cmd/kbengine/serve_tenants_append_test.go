package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

func setAlphaScope(t *testing.T, f tenantFixture, sc domain.Scope) {
	t.Helper()
	alpha, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := userauth.SetScope(context.Background(), f.store, alpha, sc); err != nil {
		t.Fatal(err)
	}
	f.h.(*tenants).scopes = userauth.NewScopes(context.Background(), f.store, nil)
}

const phoneAdded = "ADDED-FROM-PHONE-31d7"

// Токен с областью append добавляет запись и больше ничего не пишет: ни правки, ни
// удаления, ни финансов, ни ящика, ни вариантов адреса добавления (#703).
// Положительные контроли: запись добавилась и видна, у соседа с полным токеном
// правка проходит.
func TestAppendScopeAddsButNeverChanges(t *testing.T) {
	f := newTenants(t)
	setAlphaScope(t, f, domain.ScopeAppend)

	rec := do(f.h, http.MethodPost, "/api/entries",
		`{"title":"`+phoneAdded+`","category":"golang","url":"https://h/`+phoneAdded+`","verdict":"keep"}`, f.tokA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the add: %d %s", rec.Code, rec.Body.String())
	}
	if body := readable(t, get(f.h, "/api/entries", f.tokA)); !strings.Contains(body, phoneAdded) || !strings.Contains(body, secretA) {
		t.Fatalf("the added entry or the old one is not visible: %s", body)
	}

	for _, r := range registeredWrites(t) {
		if r.method == http.MethodPost && r.path == "/api/entries" {
			continue
		}
		if rec := do(f.h, r.method, r.path, `{}`, f.tokA); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with the append token: %d, want 403", r.method, r.path, rec.Code)
		}
	}
	for _, p := range []string{"/api/entries/", "//api/entries", "/api/entries/1", "/api/entriesx"} {
		if rec := do(f.h, http.MethodPost, p, `{"title":"x","category":"golang","url":"https://h/x"}`, f.tokA); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound && rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect && rec.Code != http.StatusPermanentRedirect {
			t.Errorf("POST %s with the append token: %d", p, rec.Code)
		}
	}
	if rec := do(f.h, http.MethodPatch, "/api/entries/1", `{"title":"`+editedByB+`"}`, f.tokA); rec.Code != http.StatusForbidden {
		t.Errorf("PATCH with the append token: %d, want 403", rec.Code)
	}
	if body := readable(t, get(f.h, "/api/entries", f.tokA)); strings.Contains(body, editedByB) {
		t.Error("an append token changed an entry")
	}
	if rec := do(f.h, http.MethodPatch, "/api/entries/1", `{"title":"`+editedByB+`"}`, f.tokB); rec.Code != http.StatusOK {
		t.Errorf("the neighbour's own edit: %d", rec.Code)
	}
}

// Телефон человека с append тоже только добавляет: в ящик не пишет.
func TestAppendScopeDoesNotOpenTheMailbox(t *testing.T) {
	f := newTenants(t)
	frozen(f)
	_, phone := addPhone(t, f, f.tokA)
	setAlphaScope(t, f, domain.ScopeAppend)
	for _, c := range []struct{ method, path, body, token string }{
		{http.MethodPost, "/api/mail/c-1", `{"sealed":"AAAA"}`, phone},
		{http.MethodPost, "/api/mail/c-1", `{"sealed":"AAAA"}`, f.tokA},
		{http.MethodPost, "/api/mail/devices", ``, f.tokA},
	} {
		if rec := do(f.h, c.method, c.path, c.body, c.token); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", c.method, c.path, rec.Code)
		}
	}
}

// Закрыто, а не открыто: если файл областей не прочитать, добавление тоже закрыто.
func TestAppendScopeFailsClosed(t *testing.T) {
	f := newTenants(t)
	f.h.(*tenants).scopes = userauth.NewScopes(context.Background(), scopesDown{f.store}, nil)
	body := `{"title":"x","category":"golang","url":"https://h/closed","verdict":"keep"}`
	if rec := do(f.h, http.MethodPost, "/api/entries", body, f.tokA); rec.Code != http.StatusForbidden {
		t.Errorf("add with an unreadable scopes file: %d, want 403", rec.Code)
	}
}

// Источник записи ставит сервер, а не клиент: токен append не выдает свою запись
// за пришедшую из бота (#703, ревью безопасности N2). Адрес — только http и https.
func TestAppendScopeServerOwnsSourceAndURLScheme(t *testing.T) {
	f := newTenants(t)
	setAlphaScope(t, f, domain.ScopeAppend)

	forged := `{"title":"` + phoneAdded + `","category":"golang","url":"https://h/forged","verdict":"keep","source":"bot-inbox","tags":["cheatsheet"]}`
	if rec := do(f.h, http.MethodPost, "/api/entries", forged, f.tokA); rec.Code != http.StatusCreated {
		t.Fatalf("the add: %d %s", rec.Code, rec.Body.String())
	}
	body := readable(t, get(f.h, "/api/entries", f.tokA))
	if strings.Contains(body, "bot-inbox") || !strings.Contains(body, "api-append") {
		t.Errorf("the source was not forced to api-append: %s", body)
	}

	for _, u := range []string{"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "ftp://h/x", "//h/x"} {
		rec := do(f.h, http.MethodPost, "/api/entries",
			`{"title":"x","category":"golang","url":"`+u+`","verdict":"keep"}`, f.tokA)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("url %q: %d, want 400", u, rec.Code)
		}
	}

	// Контроль: полный токен соседа source не трогают.
	own := `{"title":"B-own","category":"golang","url":"https://h/b-own","verdict":"keep","source":"manual"}`
	if rec := do(f.h, http.MethodPost, "/api/entries", own, f.tokB); rec.Code != http.StatusCreated {
		t.Fatalf("the neighbour's add: %d", rec.Code)
	}
	if body := readable(t, get(f.h, "/api/entries", f.tokB)); strings.Contains(body, "api-append") || !strings.Contains(body, "manual") {
		t.Errorf("a full token's source was changed: %s", body)
	}
}

// Предел частоты добавления на токен append: 30 подряд проходят, 31-е — 429 с
// Retry-After; через секунду снова можно. Токен с полной областью предел не знает
// (ревью безопасности N1).
func TestAppendScopeRateLimit(t *testing.T) {
	f := newTenants(t)
	now := frozen(f)
	setAlphaScope(t, f, domain.ScopeAppend)
	add := func(i int) *httptest.ResponseRecorder {
		return do(f.h, http.MethodPost, "/api/entries",
			fmt.Sprintf(`{"title":"t%d","category":"golang","url":"https://h/rl-%d","verdict":"keep"}`, i, i), f.tokA)
	}
	for i := 0; i < 30; i++ {
		if rec := add(i); rec.Code != http.StatusCreated {
			t.Fatalf("add %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := add(30)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("add over the burst: %d retry-after=%q, want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
	*now = now.Add(1100 * time.Millisecond)
	if rec := add(30 + 1); rec.Code != http.StatusCreated {
		t.Errorf("add after a second: %d", rec.Code)
	}
	for i := 0; i < 30+5; i++ {
		body := fmt.Sprintf(`{"title":"b%d","category":"golang","url":"https://h/b-%d","verdict":"keep"}`, i, i)
		if rec := do(f.h, http.MethodPost, "/api/entries", body, f.tokB); rec.Code != http.StatusCreated {
			t.Fatalf("the neighbour's add %d: %d", i, rec.Code)
		}
	}
}
