//go:build darwin

package keychain_test

import (
	"errors"
	"os/exec"
	"path/filepath"
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
// видно в ps). Кавычка, обратная косая или пробел сломали бы разбор команды,
// поэтому такой секрет — отказ, а не тихая порча.
func TestStore_RejectsUnsafeSecretAndAccount(t *testing.T) {
	s := keychain.Store{Service: "kb-desktop-test", Keychain: filepath.Join(t.TempDir(), "never.keychain")}
	for _, bad := range []string{"", `ab"cd`, `ab\cd`, "ab cd", "ab\ncd", "ab\tcd"} {
		if err := s.Set("server-ab12cd34", bad); !errors.Is(err, keychain.ErrBadValue) {
			t.Fatalf("секрет %q: err = %v, want ErrBadValue", bad, err)
		}
	}
	for _, bad := range []string{"", `a"b`, "a b"} {
		if err := s.Set(bad, "token-value"); !errors.Is(err, keychain.ErrBadValue) {
			t.Fatalf("учетка %q: err = %v, want ErrBadValue", bad, err)
		}
	}
}
