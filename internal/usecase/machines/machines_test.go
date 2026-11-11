package machines_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/machines"
)

// storeMem keeps machines in memory; failSave — запись отказывает.
type storeMem struct {
	list     []domain.Machine
	failSave bool
}

func (m *storeMem) Update(fn func([]domain.Machine) ([]domain.Machine, error)) error {
	next, err := fn(slices.Clone(m.list))
	if err != nil {
		return err
	}
	if m.failSave {
		return errors.New("диск полон")
	}
	m.list = next
	return nil
}

func (m *storeMem) Load() ([]domain.Machine, error) { return slices.Clone(m.list), nil }

// Адреса из блоков документации, имена выдуманные.
func mk(t *testing.T, name, group, host string, jump string, projects ...string) domain.Machine {
	t.Helper()
	d := domain.MachineDTO{Name: name, Group: group, Host: host, User: "ops", Auth: domain.AuthKey, Jump: jump}
	for _, p := range projects {
		d.Projects = append(d.Projects, domain.MachineProject{Name: p, Path: "/srv/" + p})
	}
	m, err := domain.NewMachine(d)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func seeded(t *testing.T) (machines.Service, *storeMem) {
	t.Helper()
	st := &storeMem{list: []domain.Machine{
		mk(t, "edge-02", "Прод", "203.0.113.7", ""),
		mk(t, "build-01", "Сборка", "10.0.0.5", "edge-02", "проект-а", "проект-б"),
		mk(t, "db-03", "Прод", "10.0.1.9", "edge-02", "проект-а-бд"),
	}}
	return machines.Service{Machines: st}, st
}

// Одна машина с одним id: повторное «добавить» — отказ, список не тронут.
func TestService_Add(t *testing.T) {
	s, st := seeded(t)
	if err := s.Add(mk(t, "cache-04", "Прод", "10.0.1.20", "")); err != nil || len(st.list) != 4 {
		t.Fatalf("добавление: %v, машин %d", err, len(st.list))
	}
	if err := s.Add(mk(t, "build-01", "Сборка", "10.0.0.99", "")); !errors.Is(err, domain.ErrMachineExists) {
		t.Fatalf("дубль id принят: %v", err)
	}
	if len(st.list) != 4 {
		t.Fatalf("после отказа машин %d, нужно 4", len(st.list))
	}
	st.failSave = true
	if err := s.Add(mk(t, "new-05", "", "10.0.0.50", "")); err == nil {
		t.Fatal("ошибка записи проглочена")
	}
}

// Найти: по проекту, по группе, пустой запрос — все; порядок один и тот же
// (по группе, потом по имени), чтобы окно и агент видели одно.
func TestService_Find(t *testing.T) {
	s, _ := seeded(t)
	for _, c := range []struct {
		q    string
		want []string
	}{
		{"", []string{"db-03", "edge-02", "build-01"}},
		{"проект-б", []string{"build-01"}},
		{"проект-а", []string{"db-03", "build-01"}},
		{"прод", []string{"db-03", "edge-02"}},
		{"нет-такого", nil},
	} {
		got, err := s.Find(c.q)
		if err != nil {
			t.Fatalf("Find(%q): %v", c.q, err)
		}
		var ids []string
		for _, m := range got {
			ids = append(ids, m.ID())
		}
		if !slices.Equal(ids, c.want) {
			t.Errorf("Find(%q) = %v, нужно %v", c.q, ids, c.want)
		}
	}
}

// Команда ssh собирается из цепочки; бастион удалили — разрыв называется.
func TestService_Command(t *testing.T) {
	s, _ := seeded(t)
	got, err := s.Command("build-01")
	if err != nil || got != "ssh -J ops@203.0.113.7:22 -p 22 ops@10.0.0.5" {
		t.Fatalf("команда: %q, %v", got, err)
	}
	if _, err := s.Command("нет-такой"); !errors.Is(err, domain.ErrMachineNotFound) {
		t.Fatalf("неизвестная машина: %v", err)
	}
	if err := s.Delete("edge-02"); err != nil {
		t.Fatalf("бастион можно удалить: %v", err)
	}
	if _, err := s.Command("build-01"); !errors.Is(err, domain.ErrMachineChain) || !strings.Contains(err.Error(), "edge-02") {
		t.Fatalf("разрыв цепочки не назван: %v", err)
	}
}

// Изменить: id остается, поля меняются; невалидная правка ничего не трогает.
func TestService_Update(t *testing.T) {
	s, st := seeded(t)
	d := st.list[1].DTO()
	d.Name = "build-01 (основная)"
	d.Projects = append(d.Projects, domain.MachineProject{Name: "проект-в", Path: "/srv/c"})
	d.ID = "кто-угодно" // id берется из адресуемой машины, а не из правки
	if err := s.Update("build-01", d); err != nil {
		t.Fatalf("правка: %v", err)
	}
	got, _ := s.Find("проект-в")
	if len(got) != 1 || got[0].ID() != "build-01" || got[0].Name() != "build-01 (основная)" {
		t.Fatalf("после правки: %+v", got)
	}
	bad := st.list[1].DTO()
	bad.Host = ""
	if err := s.Update("build-01", bad); !errors.Is(err, domain.ErrInvalidMachine) {
		t.Fatalf("невалидная правка принята: %v", err)
	}
	if err := s.Update("нет-такой", d); !errors.Is(err, domain.ErrMachineNotFound) {
		t.Fatalf("правка неизвестной: %v", err)
	}
	if h, _ := s.Find("проект-в"); len(h) != 1 || h[0].Host() != "10.0.0.5" {
		t.Fatalf("список тронут невалидной правкой: %+v", h)
	}
}

func TestService_Delete(t *testing.T) {
	s, st := seeded(t)
	if err := s.Delete("db-03"); err != nil || len(st.list) != 2 {
		t.Fatalf("удаление: %v, машин %d", err, len(st.list))
	}
	if err := s.Delete("db-03"); !errors.Is(err, domain.ErrMachineNotFound) {
		t.Fatalf("повторное удаление: %v", err)
	}
}
