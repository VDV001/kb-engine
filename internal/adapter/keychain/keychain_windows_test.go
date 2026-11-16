package keychain_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

// Настоящий Диспетчер учетных данных Windows: записи теста живут под своей
// приставкой и удаляются в конце, настоящие записи окна не трогаются.
func winStore(t *testing.T) keychain.Store {
	t.Helper()
	s := keychain.Store{Service: "kb-test", Keychain: fmt.Sprintf("kb-test-%d", os.Getpid())}
	t.Cleanup(func() {
		for _, a := range []string{"server-ab12cd34", "long"} {
			_ = s.Delete(a)
		}
	})
	return s
}

func TestWindowsCredRoundTrip(t *testing.T) {
	s := winStore(t)
	if _, err := s.Get("server-ab12cd34"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("пустое хранилище: err = %v, ждали ErrNotFound", err)
	}
	for _, secret := range []string{"tok-1", "пароль с пробелом \" и \\ и ё", "tok-2"} {
		if err := s.Set("server-ab12cd34", secret); err != nil {
			t.Fatalf("Set(%q): %v", secret, err)
		}
		if got, err := s.Get("server-ab12cd34"); err != nil || got != secret {
			t.Fatalf("Get = %q, %v; ждали %q", got, err, secret)
		}
	}
	if err := s.Delete("server-ab12cd34"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("server-ab12cd34"); err != nil {
		t.Fatalf("повторное удаление — не ошибка: %v", err)
	}
	if _, err := s.Get("server-ab12cd34"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("после удаления: %v", err)
	}
}

func TestWindowsCredLimits(t *testing.T) {
	s := winStore(t)
	if err := s.Set("long", strings.Repeat("x", 2560)); err != nil {
		t.Fatalf("2560 байт: %v", err)
	}
	if err := s.Set("long", strings.Repeat("x", 2561)); !errors.Is(err, keychain.ErrTooLong) {
		t.Fatalf("2561 байт: %v, ждали ErrTooLong", err)
	}
	for _, bad := range []string{"", "с пробелом", `a"b`, `a\b`} {
		if err := s.Set(bad, "x"); !errors.Is(err, keychain.ErrBadValue) {
			t.Fatalf("учетка %q: %v, ждали ErrBadValue", bad, err)
		}
	}
	if err := s.Set("long", ""); !errors.Is(err, keychain.ErrBadValue) {
		t.Fatalf("пустой секрет: %v", err)
	}
}
