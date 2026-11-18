package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

func addWith(t *testing.T, dir, id string, extra ...string) (int, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), id+".token")
	args := append([]string{"users", "add", "--tenants", dir, "--id", id, "--token-out", path}, extra...)
	var out, errb bytes.Buffer
	return run(args, &out, &errb), errb.String()
}

func writable(t *testing.T, dir, id string) bool {
	t.Helper()
	s, err := blobfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.NewUserID(id)
	if err != nil {
		t.Fatal(err)
	}
	return userauth.NewScopes(context.Background(), s, nil).Writable(uid)
}

// users add --scope read выпускает токен, который только читает; без флага человек
// пишет, как прежде.
func TestUsersAddScopeRead(t *testing.T) {
	dir := t.TempDir()
	if code, e := addWith(t, dir, "cloud", "--scope", "read"); code != 0 {
		t.Fatalf("add --scope read: %d %s", code, e)
	}
	if code, e := addWith(t, dir, "anna"); code != 0 {
		t.Fatalf("add: %d %s", code, e)
	}
	if writable(t, dir, "cloud") {
		t.Error("cloud may write after --scope read")
	}
	if !writable(t, dir, "anna") {
		t.Error("anna lost the write")
	}
}

// Неизвестная область — отказ до выпуска: токен не появляется.
func TestUsersAddInvalidScopeIssuesNothing(t *testing.T) {
	dir := t.TempDir()
	code, e := addWith(t, dir, "cloud", "--scope", "admin")
	if code != 2 || !strings.Contains(e, "invalid scope") || !strings.Contains(e, "read") {
		t.Errorf("exit %d, stderr %q; want 2 saying invalid scope and naming read", code, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth", "users.json")); err == nil {
		t.Error("a login was registered despite the refused scope")
	}
}

// Повторный выпуск без --scope после отзыва не наследует старое ограничение молча
// и не оставляет человека read-only по забытой записи: область называется явно.
func TestUsersAddWithoutScopeClearsTheOldOne(t *testing.T) {
	dir := t.TempDir()
	if code, e := addWith(t, dir, "cloud", "--scope", "read"); code != 0 {
		t.Fatal(e)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"users", "revoke", "--tenants", dir, "--id", "cloud"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if code, e := addWith(t, dir, "cloud"); code != 0 {
		t.Fatal(e)
	}
	if !writable(t, dir, "cloud") {
		t.Error("a re-issued token without --scope stayed read-only by a stale entry")
	}
}

// purge снимает и запись об области: имя не остается ограниченным в файле.
func TestUsersPurgeRemovesTheScope(t *testing.T) {
	dir := t.TempDir()
	if code, e := addWith(t, dir, "cloud", "--scope", "read"); code != 0 {
		t.Fatal(e)
	}
	if code, _, errb := purge("--tenants", dir, "--id", "cloud", "--confirm", "cloud", "--wait", "0s"); code != 0 {
		t.Fatalf("purge: %d %s", code, errb)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auth", "scopes.json"))
	if err == nil && strings.Contains(string(raw), `"cloud"`) {
		t.Errorf("the scope entry survived purge: %s", raw)
	}
}

func scopeCmd(dir string, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"users", "scope", "--tenants", dir}, args...), &out, &errb)
	return code, errb.String()
}

// users scope меняет область у уже заведенного человека без нового токена: так
// существующий облачный арендатор становится read-only (#701).
func TestUsersScopeChangesAnExistingPerson(t *testing.T) {
	dir := t.TempDir()
	if code, e := addWith(t, dir, "cloud"); code != 0 {
		t.Fatal(e)
	}
	if code, e := scopeCmd(dir, "--id", "cloud", "--scope", "read"); code != 0 {
		t.Fatalf("scope read: %d %s", code, e)
	}
	if writable(t, dir, "cloud") {
		t.Error("cloud may still write after users scope read")
	}
	if code, e := scopeCmd(dir, "--id", "cloud", "--scope", "write"); code != 0 {
		t.Fatalf("scope write: %d %s", code, e)
	}
	if !writable(t, dir, "cloud") {
		t.Error("users scope write did not give the write back")
	}
}

func TestUsersScopeRefusals(t *testing.T) {
	dir := t.TempDir()
	if code, e := addWith(t, dir, "cloud"); code != 0 {
		t.Fatal(e)
	}
	cases := []struct {
		name string
		args []string
		code int
		says string
	}{
		{"unknown person", []string{"--id", "ghost", "--scope", "read"}, 1, "no such user"},
		{"invalid scope", []string{"--id", "cloud", "--scope", "admin"}, 2, "invalid scope"},
		{"no scope given", []string{"--id", "cloud"}, 2, "--scope"},
	}
	for _, c := range cases {
		code, e := scopeCmd(dir, c.args...)
		if code != c.code || !strings.Contains(e, c.says) {
			t.Errorf("%s: exit %d, stderr %q; want %d naming %q", c.name, code, e, c.code, c.says)
		}
	}
	if !writable(t, dir, "cloud") {
		t.Error("a refused scope command changed something")
	}
}
