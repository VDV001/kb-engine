package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

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
