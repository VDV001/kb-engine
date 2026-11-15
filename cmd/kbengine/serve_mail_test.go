package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// sealed is a stand-in ciphertext: серверу все равно, что внутри, он хранит байты.
var sealed = base64.StdEncoding.EncodeToString([]byte("not-a-plaintext-0001"))

type mailList struct {
	Messages []struct {
		Seq    int64  `json:"seq"`
		Chat   string `json:"chat"`
		From   string `json:"from"`
		Sealed string `json:"sealed"`
	} `json:"messages"`
}

func mailOf(t *testing.T, f tenantFixture, token string, after string) mailList {
	t.Helper()
	rec := do(f.h, http.MethodGet, "/api/mail?after="+after, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/mail: %d %s", rec.Code, rec.Body.String())
	}
	var l mailList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// addPhone is the window pairing a phone: токен человека выпускает токен телефона.
func addPhone(t *testing.T, f tenantFixture, token string) (device, phone string) {
	t.Helper()
	rec := do(f.h, http.MethodPost, "/api/mail/devices", "", token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add phone: %d %s", rec.Code, rec.Body.String())
	}
	var d struct{ Device, Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || d.Token == "" {
		t.Fatalf("add phone: %s, %v", rec.Body.String(), err)
	}
	return d.Device, d.Token
}

// Телефон пишет, компьютер читает и отвечает; сторону называет токен, а не тело запроса.
func TestMailPhoneAndDesk(t *testing.T) {
	f := newTenants(t)
	_, phone := addPhone(t, f, f.tokA)
	rec := do(f.h, http.MethodPost, "/api/mail/c-1", `{"sealed":"`+sealed+`","from":"desk"}`, phone)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"seq":1`) {
		t.Fatalf("phone post: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodPost, "/api/mail/c-1", `{"sealed":"`+sealed+`"}`, f.tokA); rec.Code != http.StatusCreated {
		t.Fatalf("desk post: %d %s", rec.Code, rec.Body.String())
	}
	l := mailOf(t, f, f.tokA, "0")
	if len(l.Messages) != 2 || l.Messages[0].From != "phone" || l.Messages[1].From != "desk" || l.Messages[0].Sealed != sealed || l.Messages[0].Chat != "c-1" {
		t.Fatalf("desk reads %+v; want phone then desk, the body's «from» ignored", l.Messages)
	}
	if l := mailOf(t, f, phone, "1"); len(l.Messages) != 1 || l.Messages[0].Seq != 2 {
		t.Errorf("phone after 1: %+v", l.Messages)
	}
	if l := mailOf(t, f, f.tokB, "0"); len(l.Messages) != 0 {
		t.Errorf("B reads A's box: %+v", l.Messages)
	}
}

// Токен телефона открывает только ящик: ни базы, ни финансов, ни выпуска телефонов.
func TestMailPhoneTokenOpensOnlyTheBox(t *testing.T) {
	f := newTenants(t)
	_, phone := addPhone(t, f, f.tokA)
	for _, p := range []string{"/api/stats", "/api/entries", "/api/finances", "/kb/notes/n.md", "/"} {
		if rec := get(f.h, p, phone); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), secretA) {
			t.Errorf("phone on %s: %d", p, rec.Code)
		}
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/api/mail/devices"}, {http.MethodGet, "/api/mail/devices"}, {http.MethodDelete, "/api/mail/devices/p-1"}} {
		if rec := do(f.h, c.method, c.path, "", phone); rec.Code != http.StatusForbidden {
			t.Errorf("phone %s %s: %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", "nope"); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token on the box: %d", rec.Code)
	}
}

// Отвязанный телефон больше не входит, а список телефонов видит только их хозяин.
func TestMailRevokePhone(t *testing.T) {
	f := newTenants(t)
	dev, phone := addPhone(t, f, f.tokA)
	rec := do(f.h, http.MethodGet, "/api/mail/devices", "", f.tokA)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), dev) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodGet, "/api/mail/devices", "", f.tokB); strings.Contains(rec.Body.String(), dev) {
		t.Error("B sees A's phones")
	}
	if rec := do(f.h, http.MethodDelete, "/api/mail/devices/"+dev, "", f.tokB); rec.Code != http.StatusNotFound {
		t.Errorf("B revokes A's phone: %d, want 404", rec.Code)
	}
	if rec := do(f.h, http.MethodDelete, "/api/mail/devices/"+dev, "", f.tokA); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	f.h = tenantsHandler(t.Context(), f.store, &strings.Builder{}) // реестр перечитывается раз в минуту — новый сервер читает сразу
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked phone: %d, want 401", rec.Code)
	}
}

// Что нельзя положить в ящик.
func TestMailRefusals(t *testing.T) {
	f := newTenants(t)
	big := base64.StdEncoding.EncodeToString(make([]byte, domain.MaxMailSealed+1))
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/mail/Bad", `{"sealed":"` + sealed + `"}`, http.StatusBadRequest},
		{"/api/mail/c", `{"sealed":""}`, http.StatusBadRequest},
		{"/api/mail/c", `{"sealed":"не base64"}`, http.StatusBadRequest},
		{"/api/mail/c", `{"sealed":"` + big + `"}`, http.StatusRequestEntityTooLarge},
		{"/api/mail/c", `not json`, http.StatusBadRequest},
	} {
		if rec := do(f.h, http.MethodPost, c.path, c.body, f.tokA); rec.Code != c.want {
			t.Errorf("%s %.40s: %d, want %d", c.path, c.body, rec.Code, c.want)
		}
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=x", "", f.tokA); rec.Code != http.StatusBadRequest {
		t.Errorf("after=x: %d", rec.Code)
	}
}
