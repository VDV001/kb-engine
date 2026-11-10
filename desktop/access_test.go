package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/usecase/creds"
)

// fakeAuth stands in for kb-auth: ok — человек приложил палец; asked —
// причины, с которыми спросили; copied — что ушло в буфер.
type fakeAuth struct {
	ok     bool
	asked  []string
	copied []string
	during func() // что случилось, пока висел диалог Touch ID
}

func (f *fakeAuth) Check(reason string) error {
	f.asked = append(f.asked, reason)
	if f.during != nil {
		f.during()
	}
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
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	auth.asked = nil
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
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
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
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	d.auth = nil
	if err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "h"}, secretPW); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Reveal("crm-test-api"); err == nil || !strings.Contains(err.Error(), "kb-auth") {
		t.Fatalf("без помощника: %v", err)
	}
}

// Список и добавление — только в открытом хранилище: заметки «видны только
// в окне» и не отдаются любому JS до Touch ID; Close запирает снова.
func TestAccessDesk_sessionGatesListAndAdd(t *testing.T) {
	auth := &fakeAuth{ok: true}
	d := &accessDesk{svc: creds.Service{Cards: credsjson.Store{Path: filepath.Join(t.TempDir(), "c.json")}, Secrets: memSecrets{}}, auth: auth}
	if _, err := d.List(""); !errors.Is(err, errVaultClosed) {
		t.Fatalf("список до открытия: %v", err)
	}
	if err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "h"}, secretPW); !errors.Is(err, errVaultClosed) {
		t.Fatalf("добавление до открытия: %v", err)
	}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.List(""); err != nil {
		t.Fatalf("список после открытия: %v", err)
	}
	d.Close()
	if _, err := d.List(""); !errors.Is(err, errVaultClosed) {
		t.Fatalf("список после закрытия: %v", err)
	}
}

// fakeHelper writes a shell script standing in for kb-auth.
func fakeHelper(t *testing.T, script string) kbAuth {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kb-auth")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return kbAuth{bin: p, wait: 2 * time.Second}
}

// Коды выхода kb-auth: 0 — подтвердил; 1 — отказ; 2, сигнал — тоже не «да».
func TestKbAuth_Check(t *testing.T) {
	for _, c := range []struct {
		name   string
		script string
		ok     bool
		denied bool
	}{
		{"подтвердил", "echo ok; exit 0", true, false},
		{"отказ", "echo 'Отменено' >&2; exit 1", false, true},
		{"проверить нечем", "echo 'нет пароля' >&2; exit 2", false, false},
		{"убит сигналом", "kill -9 $$", false, false},
	} {
		err := fakeHelper(t, c.script).Check("тест")
		if (err == nil) != c.ok || errors.Is(err, errAuthDenied) != c.denied {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// Copy: «copied» — успех; другое или тишина — ошибка, и не вечное ожидание.
func TestKbAuth_Copy(t *testing.T) {
	if err := fakeHelper(t, `cat >/dev/null; echo copied; sleep 5`).Copy(secretPW); err != nil {
		t.Fatalf("copied: %v", err)
	}
	if err := fakeHelper(t, `cat >/dev/null; echo nope`).Copy(secretPW); err == nil || strings.Contains(err.Error(), "%!") {
		t.Fatalf("чужая строка: %v", err)
	}
	h := fakeHelper(t, `cat >/dev/null; sleep 30`)
	h.wait = 300 * time.Millisecond
	start := time.Now()
	if err := h.Copy(secretPW); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("зависший помощник: %v за %v", err, time.Since(start))
	}
}

// Снять доступ из окна — только в открытом хранилище и по Touch ID: удаление
// не отменить. Отказ Touch ID — карточка и пароль на месте.
func TestAccessDesk_delete(t *testing.T) {
	d, auth := newAccess(t)
	if err := d.Delete("x"); !errors.Is(err, errVaultClosed) {
		t.Fatalf("удаление в закрытом: %v", err)
	}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "h"}, secretPW); err != nil {
		t.Fatal(err)
	}
	auth.ok, auth.asked = false, nil
	if err := d.Delete("crm-test-api"); !errors.Is(err, errAuthDenied) {
		t.Fatalf("без Touch ID: %v", err)
	}
	if l, _ := d.List(""); len(l) != 1 {
		t.Fatal("карточка снята без Touch ID")
	}
	auth.ok = true
	if err := d.Delete("crm-test-api"); err != nil || len(auth.asked) != 2 {
		t.Fatalf("удаление: %v, спросили %v", err, auth.asked)
	}
	if l, _ := d.List(""); len(l) != 0 {
		t.Fatal("карточка осталась")
	}
}

// Хранилище закрылось по таймеру, пока висел диалог Touch ID, — удаление не
// проходит: решение принималось в открытом хранилище, а закрытое — уже нет.
func TestAccessDesk_delete_closedDuringTouchID(t *testing.T) {
	d, auth := newAccess(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "h"}, secretPW); err != nil {
		t.Fatal(err)
	}
	auth.during = d.Close
	if err := d.Delete("crm-test-api"); !errors.Is(err, errVaultClosed) {
		t.Fatalf("удаление после закрытия: %v", err)
	}
	auth.during = nil
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if l, _ := d.List(""); len(l) != 1 {
		t.Fatal("карточка снята в закрытом хранилище")
	}
}

// Правка карточки: по Touch ID и только в открытом хранилище; пароль необязателен
// (пусто — не менять), id остается тем же.
func TestAccessDesk_update(t *testing.T) {
	d, auth := newAccess(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "192.0.2.61", Note: "старая"}, secretPW); err != nil {
		t.Fatal(err)
	}
	id := "crm-test-api"
	auth.asked = nil
	if err := d.Update(id, accessIn{Host: "192.0.2.62", Note: "новая", IssuedAt: "2026-11-09"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(auth.asked) != 1 || !strings.Contains(auth.asked[0], "api") {
		t.Errorf("Touch ID: %q", auth.asked)
	}
	list, _ := d.List("crm")
	if len(list) != 1 || list[0].ID != id || list[0].Host != "192.0.2.62" || list[0].Note != "новая" || list[0].IssuedAt != "2026-11-09" {
		t.Fatalf("после правки: %+v", list)
	}
	if pw, _ := d.Reveal(id); pw != secretPW {
		t.Errorf("пустой пароль не должен менять прежний: %q", pw)
	}
	if err := d.Update(id, accessIn{Host: "192.0.2.62"}, "Nw9!new-pass"); err != nil {
		t.Fatal(err)
	}
	if pw, _ := d.Reveal(id); pw != "Nw9!new-pass" {
		t.Errorf("пароль не сменился: %q", pw)
	}
	auth.ok = false
	if err := d.Update(id, accessIn{Host: "192.0.2.99"}, ""); !errors.Is(err, errAuthDenied) {
		t.Errorf("без Touch ID: %v", err)
	}
	if list, _ := d.List("crm"); list[0].Host != "192.0.2.62" {
		t.Errorf("отказ Touch ID изменил карточку: %+v", list[0])
	}
	auth.ok = true
	d.Close()
	if err := d.Update(id, accessIn{Host: "h"}, ""); !errors.Is(err, errVaultClosed) {
		t.Errorf("закрытое хранилище: %v", err)
	}
}

// Среда поставлена не так: правка меняет ее, адрес карточки пересчитывается, пароль переезжает.
func TestAccessDesk_updateFixesTheEnvironment(t *testing.T) {
	d, _ := newAccess(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.Add(accessIn{Product: "Проектное бюро", Env: "prod", Login: "planb", URL: "https://x.example"}, secretPW); err != nil {
		t.Fatal(err)
	}
	if err := d.Update("proektnoe-byuro-prod-planb", accessIn{Product: "Проектное бюро", Env: "stage", Login: "planb", URL: "https://x.example"}, ""); err != nil {
		t.Fatal(err)
	}
	list, _ := d.List("planb")
	if len(list) != 1 || list[0].Env != "stage" || list[0].ID != "proektnoe-byuro-stage-planb" {
		t.Fatalf("после правки: %+v", list)
	}
	if pw, err := d.Reveal(list[0].ID); err != nil || pw != secretPW {
		t.Errorf("пароль не переехал: %q %v", pw, err)
	}
}
