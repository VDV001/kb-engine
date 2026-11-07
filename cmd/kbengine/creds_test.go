package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

// fakeKeychain stands in for the macOS Keychain: тест не трогает настоящую
// Связку владельца (урок 05.11).
type fakeKeychain map[string]string

func (k fakeKeychain) Set(a, s string) error { k[a] = s; return nil }
func (k fakeKeychain) Get(a string) (string, error) {
	if v, ok := k[a]; ok {
		return v, nil
	}
	return "", errors.New("нет записи")
}
func (k fakeKeychain) Delete(a string) error      { delete(k, a); return nil }
func (k fakeKeychain) Has(a string) (bool, error) { _, ok := k[a]; return ok, nil }

// credsEnv swaps the keychain and the clipboard for the test and returns the
// cards file and what went to the clipboard.
func credsEnv(t *testing.T) (file string, kc fakeKeychain, clip *[]string) {
	t.Helper()
	kc, clip = fakeKeychain{}, &[]string{}
	oldS, oldC := credsSecrets, credsClipboard
	credsSecrets = func() credsSecretStore { return kc }
	credsClipboard = func(s string) error { *clip = append(*clip, s); return nil }
	t.Cleanup(func() { credsSecrets, credsClipboard = oldS, oldC })
	return filepath.Join(t.TempDir(), "credentials.json"), kc, clip
}

func credsRun(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runWithStdin(append([]string{"creds"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

const secret = "Kq7!vR2m-Tz9p"

// add → find → copy: пароль приходит только из stdin, в выводе его нет ни
// разу, в буфер он уходит только по copy.
func TestCreds_addFindCopy(t *testing.T) {
	file, kc, clip := credsEnv(t)
	code, out, errs := credsRun(t, secret+"\n", "add", "--file", file, "--product", "Склад-Про", "--env", "прод", "--login", "admin.ops",
		"--url", "https://sklad.example.com", "--role", "администратор", "--by", "тимлид", "--at", "2026-10-02")
	if code != 0 || !strings.Contains(out, "sklad-pro-prod-admin-ops") {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	if kc["sklad-pro-prod-admin-ops"] != secret {
		t.Fatalf("в Связке: %v", kc)
	}
	code, out, errs = credsRun(t, "", "find", "--file", file, "склад", "прод")
	if code != 0 || !strings.Contains(out, "admin.ops") || !strings.Contains(out, "прод") || !strings.Contains(out, "https://sklad.example.com") {
		t.Fatalf("find: %d %q %q", code, out, errs)
	}
	code, out, errs = credsRun(t, "", "copy", "--file", file, "sklad-pro-prod-admin-ops")
	if code != 0 || len(*clip) != 1 || (*clip)[0] != secret || !strings.Contains(out, "30 с") {
		t.Fatalf("copy: %d %q %q, буфер %v", code, out, errs, *clip)
	}
	for _, s := range []string{out, errs} {
		if strings.Contains(s, secret) {
			t.Fatalf("пароль в выводе: %q", s)
		}
	}
}

// Отказы: повтор, нет пароля, нет карточки, неизвестная подкоманда (show
// пароля в терминал не бывает) — и ни в одном выводе пароля.
func TestCreds_refusals(t *testing.T) {
	file, _, clip := credsEnv(t)
	add := []string{"add", "--file", file, "--product", "CRM", "--env", "test", "--login", "api", "--host", "192.0.2.61"}
	if code, _, _ := credsRun(t, secret+"\n", add...); code != 0 {
		t.Fatal("первый add")
	}
	for _, c := range []struct {
		name  string
		stdin string
		args  []string
		code  int
	}{
		{"повтор", secret + "\n", add, 1},
		{"без пароля", "\n", []string{"add", "--file", file, "--product", "CRM", "--env", "test", "--login", "b", "--host", "h"}, 1},
		{"чужая среда", secret + "\n", []string{"add", "--file", file, "--product", "CRM", "--env", "qa", "--login", "c", "--host", "h"}, 1},
		{"нет карточки", "", []string{"copy", "--file", file, "nope"}, 1},
		{"copy без id", "", []string{"copy", "--file", file}, 2},
		{"show", "", []string{"show", "--file", file, "crm-test-api"}, 2},
		{"без подкоманды", "", nil, 2},
	} {
		code, out, errs := credsRun(t, c.stdin, c.args...)
		if code != c.code {
			t.Errorf("%s: код %d, нужен %d (%q %q)", c.name, code, c.code, out, errs)
		}
		if strings.Contains(out+errs, secret) {
			t.Errorf("%s: пароль в выводе", c.name)
		}
	}
	if len(*clip) != 0 {
		t.Fatalf("отказ положил в буфер: %v", *clip)
	}
}

// Буфер стирается, только если в нем все еще наш пароль: человек успел
// скопировать другое — его не трогаем.
func TestClipboardStillOurs(t *testing.T) {
	sum := clipSum(secret)
	if !clipboardStillOurs(secret, sum) || clipboardStillOurs("другое", sum) || clipboardStillOurs("", sum) {
		t.Fatal("сверка буфера")
	}
	if strings.Contains(sum, secret) {
		t.Fatal("в отпечатке сам пароль")
	}
}

// rm снимает пароль и карточку; чужой id — отказ; без id — подсказка.
func TestCreds_rm(t *testing.T) {
	file, kc, _ := credsEnv(t)
	if code, _, _ := credsRun(t, secret+"\n", "add", "--file", file, "--product", "CRM", "--env", "test", "--login", "api", "--host", "h"); code != 0 {
		t.Fatal("add")
	}
	if code, out, errs := credsRun(t, "", "rm", "--file", file, "crm-test-api"); code != 0 || !strings.Contains(out, "crm-test-api") {
		t.Fatalf("rm: %d %q %q", code, out, errs)
	}
	if _, ok := kc["crm-test-api"]; ok {
		t.Fatal("пароль остался в Связке")
	}
	if code, out, _ := credsRun(t, "", "find", "--file", file); code != 0 || !strings.Contains(out, "ничего не нашлось") {
		t.Fatalf("карточка осталась: %q", out)
	}
	if code, _, _ := credsRun(t, "", "rm", "--file", file, "crm-test-api"); code != 1 {
		t.Fatalf("повторный rm: код %d", code)
	}
	if code, _, _ := credsRun(t, "", "rm", "--file", file); code != 2 {
		t.Fatalf("rm без id: код %d", code)
	}
}

// Пароль с любыми знаками Связка теперь принимает (#494); отказ остался один —
// слишком длинный, и он говорит предел, а не обещает «пока не сохранить».
func TestCreds_tooLongSaysLimit(t *testing.T) {
	file, _, _ := credsEnv(t)
	credsSecrets = func() credsSecretStore { return tooLongKeychain{} }
	_, _, errs := credsRun(t, "long\n", "add", "--file", file, "--product", "CRM", "--env", "test", "--login", "sp", "--host", "h")
	if !strings.Contains(errs, "длинн") || strings.Contains(errs, "пока") || strings.Contains(errs, "пробел") {
		t.Fatalf("отказ: %q", errs)
	}
}

// tooLongKeychain refuses values like security -i does past its line limit.
type tooLongKeychain struct{ fakeKeychain }

func (tooLongKeychain) Has(string) (bool, error) { return false, nil }
func (tooLongKeychain) Set(string, string) error { return keychain.ErrTooLong }
