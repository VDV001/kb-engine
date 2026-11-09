//go:build darwin

package keychain_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

// tempStore — связка ключей во временном каталоге теста: проверка не трогает
// login-связку владельца и не оставляет следов.
func tempStore(t *testing.T) keychain.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.keychain")
	if out, err := exec.Command("security", "create-keychain", "-p", "test", path).CombinedOutput(); err != nil {
		t.Fatalf("create-keychain: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("security", "delete-keychain", path).Run() })
	return keychain.Store{Service: "kb-desktop-test", Keychain: path}
}

func TestStore_SetGetDelete(t *testing.T) {
	s := tempStore(t)
	const tok = "tok+/=abcXYZ0123456789abcdefghijklmn"
	if _, err := s.Get("server-ab12cd34"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("пустая связка: err = %v, want ErrNotFound", err)
	}
	if err := s.Set("server-ab12cd34", tok); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("server-ab12cd34"); err != nil || got != tok {
		t.Fatalf("Get = %q, %v", got, err)
	}
	// Повторная запись заменяет, а не падает «уже есть».
	if err := s.Set("server-ab12cd34", tok+"2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("server-ab12cd34"); got != tok+"2" {
		t.Fatalf("после замены = %q", got)
	}
	if err := s.Delete("server-ab12cd34"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("server-ab12cd34"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("после удаления: %v", err)
	}
	// Удалить отсутствующее — не ошибка: убрать источник без токена можно.
	if err := s.Delete("server-ab12cd34"); err != nil {
		t.Fatalf("повторное удаление: %v", err)
	}
}

// Секрет идет в security через stdin в режиме -i, а не аргументом (аргумент
// видно в ps), и hex-строкой (-X): пробел, кавычка, обратная косая, не-ASCII
// и перевод строки не ломают разбор команды. Чтение различает печатное и hex
// (замер 07.11: -w отдает «deadbeef» и hex от «пароль» одинаково).
func TestStore_anyCharacters(t *testing.T) {
	s := tempStore(t)
	for _, v := range []string{
		"ab cd", `ab"cd`, `ab\cd`, "пароль", "ab\u00a0cd", "ab\tcd", "ab\ncd",
		`"edge"`, "end space ", " lead", "deadbeef", "0x41", `a b"c\d е`,
	} {
		if err := s.Set("server-ab12cd34", v); err != nil {
			t.Fatalf("Set %q: %v", v, err)
		}
		if got, err := s.Get("server-ab12cd34"); err != nil || got != v {
			t.Fatalf("Get после %q = %q, %v", v, got, err)
		}
	}
}

// Строка security -i — до 4096 знаков (замер 07.11), hex удваивает пароль:
// длиннее — отказ словами, а не «запись не сохранилась».
func TestStore_tooLong(t *testing.T) {
	s := tempStore(t)
	if err := s.Set("server-ab12cd34", strings.Repeat("я", 950)); err != nil {
		t.Fatalf("1900 байт: %v", err)
	}
	if err := s.Set("server-ab12cd34", strings.Repeat("z", 1901)); !errors.Is(err, keychain.ErrTooLong) {
		t.Fatalf("1901 байт: err = %v, want ErrTooLong", err)
	}
}

// Пустой секрет и учетка, которая сломала бы разбор, — отказ, а не тихая порча.
func TestStore_RejectsUnsafeSecretAndAccount(t *testing.T) {
	s := keychain.Store{Service: "kb-desktop-test", Keychain: filepath.Join(t.TempDir(), "never.keychain")}
	if err := s.Set("server-ab12cd34", ""); !errors.Is(err, keychain.ErrBadValue) {
		t.Fatalf("пустой секрет: err = %v, want ErrBadValue", err)
	}
	for _, bad := range []string{"", `a"b`, "a b"} {
		if err := s.Set(bad, "token-value"); !errors.Is(err, keychain.ErrBadValue) {
			t.Fatalf("учетка %q: err = %v, want ErrBadValue", bad, err)
		}
	}
}

// Пустой пароль, заведенный в Связке руками или другой программой, читается
// пустым, а не ошибкой: -g печатает для него "password: " без кавычек.
func TestStore_emptyPasswordReadsEmpty(t *testing.T) {
	s := tempStore(t)
	if out, err := exec.Command("security", "add-generic-password", "-s", s.Service, "-a", "server-ab12cd34", "-w", "", s.Keychain).CombinedOutput(); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if got, err := s.Get("server-ab12cd34"); err != nil || got != "" {
		t.Fatalf("Get = %q, %v", got, err)
	}
}
