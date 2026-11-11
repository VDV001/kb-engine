package credsjson_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/domain"
)

func machine(t *testing.T, name string) domain.Machine {
	t.Helper()
	m, err := domain.NewMachine(domain.MachineDTO{Name: name, Host: "10.0.0.5", User: "deploy", Auth: domain.AuthKey, KeyPath: "~/.ssh/k",
		Projects: []domain.MachineProject{{Name: "проект-а", Path: "/srv/a"}}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func addMachine(m domain.Machine) func([]domain.Machine) ([]domain.Machine, error) {
	return func(l []domain.Machine) ([]domain.Machine, error) { return append(l, m), nil }
}

// Машины живут отдельным файлом рядом с карточками доступов, тоже только для
// своего пользователя (0600): файла нет — пусто; записанное читается обратно
// вместе с проектами; ошибка fn файл не трогает; пароля в файле нет.
func TestMachineStore_roundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb-desktop", "machines.json")
	s := credsjson.MachineStore{Path: path}
	if got, err := s.Load(); err != nil || len(got) != 0 {
		t.Fatalf("нет файла: %v, %v", got, err)
	}
	if err := s.Update(addMachine(machine(t, "build-01"))); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права файла: %v, %v", st, err)
	}
	boom := errors.New("нет")
	if err := s.Update(func([]domain.Machine) ([]domain.Machine, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("ошибка fn: %v", err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 1 || got[0].ID() != "build-01" || len(got[0].Projects()) != 1 || got[0].KeyPath() != "~/.ssh/k" {
		t.Fatalf("прочитано %+v, %v", got, err)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(strings.ToLower(string(raw)), "pass") {
		t.Fatalf("в файле машин слово про пароль: %s", raw)
	}
}

// Файл машин и файл учеток не мешают друг другу: разные файлы в одном каталоге.
func TestMachineStore_besideCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kb-desktop")
	if err := (credsjson.Store{Path: filepath.Join(dir, "credentials.json")}).Update(add(card(t, "admin"))); err != nil {
		t.Fatal(err)
	}
	if err := (credsjson.MachineStore{Path: filepath.Join(dir, "machines.json")}).Update(addMachine(machine(t, "build-01"))); err != nil {
		t.Fatal(err)
	}
	creds, err := credsjson.Store{Path: filepath.Join(dir, "credentials.json")}.Load()
	if err != nil || len(creds) != 1 {
		t.Fatalf("учетки пропали или перемешались: %v, %v", creds, err)
	}
}

// Два «добавить» разом не теряют машину: чтение и запись под одним замком.
func TestMachineStore_concurrentUpdates(t *testing.T) {
	s := credsjson.MachineStore{Path: filepath.Join(t.TempDir(), "machines.json")}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Update(addMachine(machine(t, fmt.Sprintf("m-%d", i)))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, err := s.Load(); err != nil || len(got) != 8 {
		t.Fatalf("после 8 записей машин %d: %v", len(got), err)
	}
}

// Битый файл — ошибка с путем, а не пустой список: пустой список на записи
// затер бы все машины. Карточка, нарушающая инвариант, тоже ошибка.
func TestMachineStore_brokenFile(t *testing.T) {
	for name, content := range map[string]string{
		"не json":            "{нет",
		"нет хоста":          `[{"name":"x","user":"u"}]`,
		"опция вместо хоста": `[{"name":"x","host":"-oProxyCommand=y","user":"u"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "machines.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := credsjson.MachineStore{Path: path}.Load()
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("ошибка без пути к файлу: %v", err)
			}
			if err := (credsjson.MachineStore{Path: path}).Update(addMachine(machine(t, "build-01"))); err == nil {
				t.Fatal("запись поверх битого файла прошла")
			}
			if raw, _ := os.ReadFile(path); string(raw) != content {
				t.Fatalf("битый файл тронут: %s", raw)
			}
		})
	}
}
