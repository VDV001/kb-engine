package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/usecase/creds"
)

// fakeAuth stands in for kb-auth: ok — человек приложил палец; asked —
// причины, с которыми спросили; copied — что ушло в буфер.
type fakeAuth struct {
	ok     bool
	asked  []string
	copied []string
}

func (f *fakeAuth) Check(reason string) error {
	f.asked = append(f.asked, reason)
	if !f.ok {
		return errAuthDenied
	}
	return nil
}

func (f *fakeAuth) Copy(secret string) error { f.copied = append(f.copied, secret); return nil }

type memSecrets map[string]string

func (m memSecrets) Has(a string) (bool, error) { _, ok := m[a]; return ok, nil }
func (m memSecrets) Set(a, s string) error      { m[a] = s; return nil }
func (m memSecrets) Get(a string) (string, error) {
	if v, ok := m[a]; ok {
		return v, nil
	}
	return "", errors.New("нет записи")
}
func (m memSecrets) Delete(a string) error { delete(m, a); return nil }

func newAccess(t *testing.T) (*accessDesk, *fakeAuth) {
	t.Helper()
	auth := &fakeAuth{ok: true}
	svc := creds.Service{Cards: credsjson.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}, Secrets: memSecrets{}}
	return &accessDesk{svc: svc, auth: auth}, auth
}

const secretPW = "Kq7!vR2m-Tz9p"

// Добавить из окна → список без пароля; показать и скопировать — только после
// Touch ID, каждый раз; отказ Touch ID — ни пароля, ни буфера.
func TestAccessDesk(t *testing.T) {
	d, auth := newAccess(t)
	if err := d.Add(accessIn{Product: "Склад-Про", Env: "прод", Login: "admin.ops", URL: "https://sklad.example.com", Role: "администратор", Note: "только из офиса"}, secretPW); err != nil {
		t.Fatal(err)
	}
	list, err := d.List("склад")
	if err != nil || len(list) != 1 {
		t.Fatalf("список %+v, %v", list, err)
	}
	c := list[0]
	if c.ID != "sklad-pro-prod-admin-ops" || c.Env != "prod" || c.EnvName != "прод" || !c.Risky || c.Note != "только из офиса" {
		t.Fatalf("карточка %+v", c)
	}
	if strings.Contains(strings.Join([]string{c.ID, c.Product, c.URL, c.Login, c.Role, c.Note}, " "), secretPW) {
		t.Fatal("пароль в карточке окна")
	}
	if len(auth.asked) != 0 {
		t.Fatalf("Touch ID спросили без пароля: %v", auth.asked)
	}
	pw, err := d.Reveal(c.ID)
	if err != nil || pw != secretPW || len(auth.asked) != 1 {
		t.Fatalf("показать: %q, %v, спросили %v", pw, err, auth.asked)
	}
	if err := d.Copy(c.ID); err != nil || len(auth.copied) != 1 || auth.copied[0] != secretPW || len(auth.asked) != 2 {
		t.Fatalf("копия: %v, буфер %v, спросили %v", err, auth.copied, auth.asked)
	}
	auth.ok = false
	if pw, err := d.Reveal(c.ID); !errors.Is(err, errAuthDenied) || pw != "" {
		t.Fatalf("показ без Touch ID: %q, %v", pw, err)
	}
	if err := d.Copy(c.ID); !errors.Is(err, errAuthDenied) || len(auth.copied) != 1 {
		t.Fatalf("копия без Touch ID: %v, буфер %v", err, auth.copied)
	}
	if err := d.Open(); !errors.Is(err, errAuthDenied) {
		t.Fatalf("открыть без Touch ID: %v", err)
	}
}

// Отказы добавления понятны окну: повтор, чужая среда, пустой пароль.
func TestAccessDesk_addRefusals(t *testing.T) {
	d, _ := newAccess(t)
	in := accessIn{Product: "CRM", Env: "test", Login: "api", Host: "192.0.2.61"}
	if err := d.Add(in, secretPW); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		in accessIn
		pw string
	}{
		"повтор":      {in, secretPW},
		"чужая среда": {accessIn{Product: "CRM", Env: "qa", Login: "b", Host: "h"}, secretPW},
		"без пароля":  {accessIn{Product: "CRM", Env: "test", Login: "c", Host: "h"}, "  "},
	} {
		err := d.Add(c.in, c.pw)
		if err == nil || strings.Contains(err.Error(), secretPW) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Без помощника kb-auth (окно не из сборки) пароль не показать: Touch ID
// подделать нельзя, и окно говорит, почему.
func TestAccessDesk_noHelper(t *testing.T) {
	d, _ := newAccess(t)
	d.auth = nil
	if err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "h"}, secretPW); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Reveal("crm-test-api"); err == nil || !strings.Contains(err.Error(), "kb-auth") {
		t.Fatalf("без помощника: %v", err)
	}
}
