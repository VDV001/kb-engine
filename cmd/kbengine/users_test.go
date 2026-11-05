package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
)

// flakyStore fails the registry once: lieOnPut — записать и ответить ошибкой
// (S3, у которого ответ потерялся по таймауту), failGet — не прочитать.
// Второй раз все проходит, как у живого хранилища после сбоя.
type flakyStore struct {
	blob.Store
	lieOnPut, failGet *bool
}

func (f flakyStore) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	etag, err := f.Store.Put(ctx, key, data, ifMatch)
	if err == nil && key == userauth.RegistryKey && *f.lieOnPut {
		*f.lieOnPut = false
		return "", errors.New("put auth/users.json: context deadline exceeded")
	}
	return etag, err
}

func (f flakyStore) Get(ctx context.Context, key string) (blob.Object, error) {
	if key == userauth.RegistryKey && *f.failGet {
		*f.failGet = false
		return blob.Object{}, errors.New("get auth/users.json: context deadline exceeded")
	}
	return f.Store.Get(ctx, key)
}

// withFlakyStore makes users commands open a flakyStore over their store.
func withFlakyStore(t *testing.T, lieOnPut, failGet bool) {
	t.Helper()
	prev := usersOpenStore
	usersOpenStore = func(loc, tokenFile string) (blob.Store, error) {
		s, err := prev(loc, tokenFile)
		return flakyStore{Store: s, lieOnPut: &lieOnPut, failGet: &failGet}, err
	}
	t.Cleanup(func() { usersOpenStore = prev })
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

// Хранилище записало хэш и ответило ошибкой: вход отзывается, иначе хэш
// остался бы без токена, а повторный add ответил бы «уже есть».
func TestUsersAddRevokesWhenTheStoreAnswersAfterWriting(t *testing.T) {
	dir := t.TempDir()
	withFlakyStore(t, true, false)
	path := filepath.Join(t.TempDir(), "anna.token")
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna", "--token-out", path}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1; stderr %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "вход anna закрыт") || strings.Contains(errb.String(), "не закрылся") {
		t.Errorf("stderr %q, want the login reported closed", errb.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("token file left behind: %v", err)
	}
	addToFile(t, dir, "anna") // хэш отозван, имя свободно
}

// Сбой до проверки реестра у человека, который уже есть: его рабочий вход
// остается. Откат знает свой токен и чужую запись не трогает.
func TestUsersAddFailureKeepsAnExistingLogin(t *testing.T) {
	dir := t.TempDir()
	old := addToFile(t, dir, "anna")
	withFlakyStore(t, false, true)
	var out, errb bytes.Buffer
	path := filepath.Join(t.TempDir(), "again.token")
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna", "--token-out", path}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1; stderr %s", code, errb.String())
	}
	if _, ok := lookup(t, dir, old); !ok {
		t.Fatalf("a failed add closed anna's working login; stderr %s", errb.String())
	}
}

// Подсказка — готовая команда: настоящий путь к файлу, в кавычках.
func TestUsersAddHintNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "my tokens", "anna.token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna", "--token-out", path}, &out, &errb); code != 0 {
		t.Fatalf("exit %d; stderr %s", code, errb.String())
	}
	if want := "pbcopy < '" + path + "'"; !strings.Contains(errb.String(), want) {
		t.Errorf("stderr %q, want %q", errb.String(), want)
	}
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
		{[]string{"users", "add", "--tenants", dir, "--id", "bob"}, 2, "--token-out"},
		// Флага нет: Связку ведет окно. Имя с пробелом — чтобы и код до этой
		// правки не дошел до записи в настоящую Связку.
		{[]string{"users", "add", "--tenants", dir, "--id", "bob", "--keychain", "not a source"}, 2, "keychain"},
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
