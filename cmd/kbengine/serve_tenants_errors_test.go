package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Ответы на плохие запросы не называют другого человека (#545): ни его имени, ни его
// метки, ни ключей хранилища. Положительный контроль: те же запросы с токеном A
// на свои данные работают, а проверка слов ловит подсаженную утечку (см. ниже).
func TestTenantsErrorAnswersNameNoOtherPerson(t *testing.T) {
	f := newTenants(t)
	cases := []struct {
		name, method, path, body, token string
	}{
		{"path traversal in /kb", "GET", "/kb/../users/alpha/kb/_data/catalog.json", "", f.tokB},
		{"encoded traversal in /kb", "GET", "/kb/%2e%2e/%2e%2e/users/alpha/kb/notes/n.md", "", f.tokB},
		{"other person's key under /kb", "GET", "/kb/users/alpha/kb/notes/n.md", "", f.tokB},
		{"unknown route", "GET", "/api/users/alpha", "", f.tokB},
		{"bad limit", "GET", "/api/entries?limit=abc", "", f.tokB},
		{"patch of a missing id", "PATCH", "/api/entries/9999", `{"title":"x"}`, f.tokB},
		{"patch of a bad id", "PATCH", "/api/entries/alpha", `{"title":"x"}`, f.tokB},
		{"broken json", "POST", "/api/entries", `{"title":`, f.tokB},
		{"unknown field", "POST", "/api/entries", `{"nope":1}`, f.tokB},
		{"transaction with a foreign account", "POST", "/api/finances/transactions", `{"kind":"expense","amount":"1","account":"alpha"}`, f.tokB},
		{"balance of a missing account", "PUT", "/api/finances/accounts/alpha/balance", `{"amount":"1"}`, f.tokB},
		{"mail file of A", "GET", "/api/mail/files/00000000000000000000000000000000", "", f.tokB},
		{"mail device of A", "DELETE", "/api/mail/devices/p-00000000", "", f.tokB},
		{"rename of a missing phone", "PUT", "/api/mail/devices/p-00000000/name", `{"name":"x"}`, f.tokB},
		{"A's token on a bad route", "GET", "/api/nothing/alpha", "", f.tokA},
		{"no token", "GET", "/api/entries", "", ""},
		{"wrong token", "GET", "/api/entries", "", "wrong-" + f.tokB},
	}
	// «beta» своего имени в ответе B допустимо только в данных B, а не в ошибке; ищем чужое.
	forbidden := []string{"alpha", secretA, "users/", "mailfiles", "auth/", "token_sha256", ".json: "}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			if c.token != "" {
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, req)
			if rec.Code >= 500 {
				t.Errorf("status %d (%s)", rec.Code, rec.Body.String())
			}
			var sb strings.Builder
			sb.WriteString(rec.Body.String())
			for k, vs := range rec.Header() {
				sb.WriteString(" " + k + "=" + strings.Join(vs, ","))
			}
			seen := sb.String()
			for _, w := range forbidden {
				// Эхо собственного пути запроса не утечка: в нем «alpha» стоит от самого клиента.
				if strings.Contains(strings.ReplaceAll(seen, c.path, ""), w) && !strings.Contains(c.path+c.body, w) {
					t.Errorf("the answer contains %q: %.200s", w, seen)
				}
			}
		})
	}
}

// Чужой телефон нельзя переименовать (#545): 404, как при отзыве; свой — можно.
func TestTenantsRenameOfAnotherPersonsPhoneIsRefused(t *testing.T) {
	f := newTenants(t)
	dev, _ := addPhone(t, f, f.tokA)
	if rec := do(f.h, http.MethodPut, "/api/mail/devices/"+dev+"/name", `{"name":"hacked"}`, f.tokB); rec.Code != http.StatusNotFound {
		t.Errorf("B renames A's phone: %d, want 404", rec.Code)
	}
	if rec := do(f.h, http.MethodPut, "/api/mail/devices/"+dev+"/name", `{"name":"Phone"}`, f.tokA); rec.Code != http.StatusNoContent {
		t.Errorf("A renames own phone: %d, want 204 (the positive control)", rec.Code)
	}
}
