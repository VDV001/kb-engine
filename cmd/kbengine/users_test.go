package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
)

// fakeKeychain stands in for the Связка ключей: тесты не пишут в настоящую.
type fakeKeychain struct {
	got map[string]string
	err error
}

func (f *fakeKeychain) Set(account, secret string) error {
	if f.err != nil {
		return f.err
	}
	f.got[account] = secret
	return nil
}

func withFakeKeychain(t *testing.T, err error) *fakeKeychain {
	t.Helper()
	f := &fakeKeychain{got: map[string]string{}, err: err}
	prev := usersKeychain
	usersKeychain = func() secretSetter { return f }
	t.Cleanup(func() { usersKeychain = prev })
	return f
}

func lookup(t *testing.T, dir, tok string) (string, bool) {
	t.Helper()
	s, err := blobfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := userauth.NewRegistry(context.Background(), s).Lookup(tok)
	return id.String(), ok
}

// addToFile issues a token for id into a fresh file and returns the token.
func addToFile(t *testing.T, dir, id string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), id+".token")
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", id, "--token-out", path}, &out, &errb); code != 0 {
		t.Fatalf("users add: exit %d, stderr %s", code, errb.String())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// Токен не выходит в терминал никогда: вывод терминала попадает в историю
// оболочки, в журнал агента и в переписку с моделью, а токен открывает
// базу и финансы человека.
func TestUsersAddNeverPrintsTheToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "anna.token")
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna", "--token-out", path}, &out, &errb); code != 0 {
		t.Fatalf("users add: exit %d, stderr %s", code, errb.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("token file mode %o, want 600", mode)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(string(b))
	if got, ok := lookup(t, dir, tok); !ok || got != "anna" {
		t.Fatalf("Lookup(token from file) = %q, %v", got, ok)
	}
	if out.Len() != 0 {
		t.Errorf("stdout %q, want nothing", out.String())
	}
	if strings.Contains(errb.String(), tok) {
		t.Error("stderr carries the token")
	}
}

func TestUsersAddPutsTheTokenIntoTheKeychain(t *testing.T) {
	kc := withFakeKeychain(t, nil)
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna", "--keychain", "server-0a1b2c3d"}, &out, &errb); code != 0 {
		t.Fatalf("users add: exit %d, stderr %s", code, errb.String())
	}
	tok := kc.got["server-0a1b2c3d"]
	if got, ok := lookup(t, dir, tok); !ok || got != "anna" {
		t.Fatalf("Lookup(token from keychain) = %q, %v", got, ok)
	}
	if out.Len() != 0 || strings.Contains(errb.String(), tok) {
		t.Errorf("the token reached the terminal: stdout %q, stderr %q", out.String(), errb.String())
	}
}

// Связка отказала — токен не сохранен нигде, кроме хэша. Такой вход закрыт
// сразу: иначе человек остался бы с хэшем, к которому нет токена, и
// повторный add отказал бы «уже есть».
func TestUsersAddRevokesWhenTheKeychainRefuses(t *testing.T) {
	withFakeKeychain(t, errors.New("user canceled"))
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna", "--keychain", "server-0a1b2c3d"}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1; stderr %s", code, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout %q on a refusal", out.String())
	}
	addToFile(t, dir, "anna") // вход закрыт, имя свободно для нового токена
}

func TestUsersCommandRefusals(t *testing.T) {
	dir := t.TempDir()
	addToFile(t, dir, "anna")
	taken := filepath.Join(t.TempDir(), "taken.token")
	if err := os.WriteFile(taken, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := func() string { return filepath.Join(t.TempDir(), "x.token") }
	cases := []struct {
		args []string
		code int
		says string
	}{
		{[]string{"users", "add", "--tenants", dir, "--id", "anna", "--token-out", fresh()}, 1, "already exists"},
		{[]string{"users", "add", "--tenants", dir, "--id", "Анна", "--token-out", fresh()}, 2, "invalid user id"},
		{[]string{"users", "add", "--id", "bob", "--token-out", fresh()}, 2, "--tenants"},
		{[]string{"users", "add", "--tenants", dir, "--id", "bob"}, 2, "--keychain"},
		{[]string{"users", "add", "--tenants", dir, "--id", "bob", "--token-out", fresh(), "--keychain", "server-0a1b2c3d"}, 2, "--keychain"},
		{[]string{"users", "add", "--tenants", dir, "--id", "bob", "--token-out", taken}, 2, "exists"},
		{[]string{"users", "revoke", "--tenants", dir, "--id", "ghost"}, 1, "no such user"},
		{[]string{"users", "rename", "--tenants", dir, "--id", "anna"}, 2, "add"},
		{[]string{"users"}, 2, "add"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := run(c.args, &out, &errb)
		if code != c.code || !strings.Contains(errb.String(), c.says) {
			t.Errorf("%v: exit %d, stderr %q; want exit %d naming %q", c.args, code, errb.String(), c.code, c.says)
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout %q on a refusal", c.args, out.String())
		}
	}
	// Отказ по месту для токена случается до выпуска: bob не заведен, и
	// чужой файл не тронут.
	addToFile(t, dir, "bob")
	if b, _ := os.ReadFile(taken); string(b) != "keep me" {
		t.Errorf("existing file overwritten: %q", b)
	}
}

func TestUsersRevokeClosesTheDoor(t *testing.T) {
	dir := t.TempDir()
	tok := addToFile(t, dir, "anna")
	var out, errb bytes.Buffer
	if code := run([]string{"users", "revoke", "--tenants", dir, "--id", "anna"}, &out, &errb); code != 0 {
		t.Fatalf("revoke: exit %d, %s", code, errb.String())
	}
	if _, ok := lookup(t, dir, tok); ok {
		t.Fatal("revoked token still admitted")
	}
}
