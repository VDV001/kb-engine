package credsjson_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/domain"
)

func card(t *testing.T, login string) domain.Credential {
	t.Helper()
	c, err := domain.NewCredential(domain.CredentialDTO{Product: "Склад-Про", Env: domain.EnvProd, Login: login, Host: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func add(c domain.Credential) func([]domain.Credential) ([]domain.Credential, error) {
	return func(l []domain.Credential) ([]domain.Credential, error) { return append(l, c), nil }
}

// Карточки — файлом только для своего пользователя (0600), в каталоге 0700;
// файла нет — пусто; записанное читается обратно; ошибка fn файл не трогает.
func TestStore_roundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb-desktop", "credentials.json")
	s := credsjson.Store{Path: path}
	if got, err := s.Load(); err != nil || len(got) != 0 {
		t.Fatalf("нет файла: %v, %v", got, err)
	}
	if err := s.Update(add(card(t, "admin"))); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права файла: %v, %v", st, err)
	}
	if d, _ := os.Stat(filepath.Dir(path)); d.Mode().Perm() != 0o700 {
		t.Fatalf("права каталога: %v", d.Mode().Perm())
	}
	boom := errors.New("нет")
	if err := s.Update(func([]domain.Credential) ([]domain.Credential, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("ошибка fn: %v", err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 1 || got[0].ID() != "sklad-pro-prod-admin" || got[0].Env() != domain.EnvProd {
		t.Fatalf("прочитано %+v, %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(strings.ToLower(string(raw)), "pass") {
		t.Fatalf("в файле карточек слово про пароль: %s", raw)
	}
}

// Два «добавить» разом не теряют карточку: чтение и запись под одним замком.
func TestStore_concurrentUpdates(t *testing.T) {
	s := credsjson.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	var wg sync.WaitGroup
	for _, l := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"} {
		wg.Go(func() {
			if err := s.Update(add(card(t, l))); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got, _ := s.Load(); len(got) != 8 {
		t.Fatalf("карточек %d из 8", len(got))
	}
}

// Битый файл — ошибка с путем, а не пустой список: пустой список на записи
// затер бы все карточки.
func TestStore_brokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{обрыв"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := credsjson.Store{Path: path}
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("битый файл: %v", err)
	}
	if err := s.Update(add(card(t, "x"))); err == nil {
		t.Fatal("запись поверх битого файла")
	}
	if raw, _ := os.ReadFile(path); string(raw) != "{обрыв" {
		t.Fatalf("битый файл перезаписан: %s", raw)
	}
}
