package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/creds"
	"github.com/daniil/kb-engine/internal/usecase/machines"
)

func newMachineDesk(t *testing.T) (*accessDesk, *fakeAuth, memSecrets) {
	t.Helper()
	dir, sec, auth := t.TempDir(), memSecrets{}, &fakeAuth{ok: true}
	return &accessDesk{
		svc:      creds.Service{Cards: credsjson.Store{Path: filepath.Join(dir, "credentials.json")}, Secrets: sec},
		machines: machines.Service{Machines: credsjson.MachineStore{Path: filepath.Join(dir, "machines.json")}},
		auth:     auth,
	}, auth, sec
}

// Адреса из блоков документации, пароль и пути выдуманные.
const machinePW = "Zr4!nB8q-Lw2x"

func buildIn() machineIn {
	return machineIn{Name: "build-01", Group: "Сборка", Env: "стенд", Host: "10.0.0.5", User: "deploy", Auth: "password",
		Projects: []domain.MachineProject{{Name: "проект-а", Path: "/srv/a"}}}
}

// Пока хранилище закрыто, с машинами нельзя ничего: ни список, ни запись.
func TestMachineDesk_vaultClosed(t *testing.T) {
	d, _, _ := newMachineDesk(t)
	if _, err := d.Machines(""); !errors.Is(err, errVaultClosed) {
		t.Errorf("список: %v", err)
	}
	if err := d.AddMachine(buildIn(), machinePW); !errors.Is(err, errVaultClosed) {
		t.Errorf("добавление: %v", err)
	}
	if err := d.UpdateMachine("build-01", buildIn(), ""); !errors.Is(err, errVaultClosed) {
		t.Errorf("правка: %v", err)
	}
	if err := d.DeleteMachine("build-01"); !errors.Is(err, errVaultClosed) {
		t.Errorf("удаление: %v", err)
	}
}

// Добавить → список без пароля, но с командой ssh и признаком «пароль есть»;
// показать и скопировать пароль — только после Touch ID, каждый раз; отказ
// Touch ID — ни пароля, ни буфера.
func TestMachineDesk_passwordOnlyByTouchID(t *testing.T) {
	d, auth, _ := newMachineDesk(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	auth.asked = nil
	if err := d.AddMachine(buildIn(), machinePW); err != nil {
		t.Fatal(err)
	}
	list, err := d.Machines("проект-а")
	if err != nil || len(list) != 1 {
		t.Fatalf("список %+v, %v", list, err)
	}
	v := list[0]
	if v.ID != "build-01" || v.Command != "ssh -p 22 deploy@10.0.0.5" || !v.HasPassword || v.EnvName != "стенд" {
		t.Fatalf("карточка %+v", v)
	}
	if strings.Contains(strings.Join([]string{v.ID, v.Name, v.Host, v.User, v.Command, v.Note, v.KeyPath}, " "), machinePW) {
		t.Fatal("пароль в карточке окна")
	}
	if len(auth.asked) != 0 {
		t.Fatalf("список спросил Touch ID: %v", auth.asked)
	}
	got, err := d.RevealMachinePassword("build-01")
	if err != nil || got != machinePW {
		t.Fatalf("показать: %q, %v", got, err)
	}
	if err := d.CopyMachinePassword("build-01"); err != nil || !slices.Equal(auth.copied, []string{machinePW}) {
		t.Fatalf("скопировать: %v, буфер %v", err, auth.copied)
	}
	if len(auth.asked) != 2 {
		t.Fatalf("Touch ID спрошен %d раз, нужно 2: %v", len(auth.asked), auth.asked)
	}
	auth.ok, auth.copied = false, nil
	if pw, err := d.RevealMachinePassword("build-01"); err == nil || pw != "" {
		t.Fatalf("отказ Touch ID отдал пароль: %q, %v", pw, err)
	}
	if err := d.CopyMachinePassword("build-01"); err == nil || len(auth.copied) != 0 {
		t.Fatalf("отказ Touch ID положил в буфер: %v, %v", err, auth.copied)
	}
}

// Пароль нужен только при входе по паролю; без него при входе по паролю — отказ.
func TestMachineDesk_passwordRules(t *testing.T) {
	for _, c := range []struct {
		name string
		auth string
		pw   string
	}{
		{"пароль без пароля", "password", ""},
		{"ключ с паролем", "key", machinePW},
		{"токен с паролем", "token", machinePW},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, _, sec := newMachineDesk(t)
			if err := d.Open(); err != nil {
				t.Fatal(err)
			}
			in := buildIn()
			in.Auth = c.auth
			if err := d.AddMachine(in, c.pw); err == nil {
				t.Fatal("принято")
			}
			if list, _ := d.Machines(""); len(list) != 0 || len(sec) != 0 {
				t.Fatalf("осталось: машин %d, паролей %d", len(list), len(sec))
			}
		})
	}
}

// Дубль id не затирает чужой пароль: прежний остается, новый не ложится.
func TestMachineDesk_duplicateKeepsOldPassword(t *testing.T) {
	d, _, _ := newMachineDesk(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMachine(buildIn(), machinePW); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMachine(buildIn(), "Другой-Пароль-1!"); !errors.Is(err, domain.ErrMachineExists) {
		t.Fatalf("дубль принят: %v", err)
	}
	if got, err := d.RevealMachinePassword("build-01"); err != nil || got != machinePW {
		t.Fatalf("пароль после дубля: %q, %v", got, err)
	}
}

// Правка: пустой пароль оставляет прежний, новый заменяет.
func TestMachineDesk_update(t *testing.T) {
	d, _, _ := newMachineDesk(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMachine(buildIn(), machinePW); err != nil {
		t.Fatal(err)
	}
	in := buildIn()
	in.Projects = append(in.Projects, domain.MachineProject{Name: "проект-б", Path: "/srv/b"})
	if err := d.UpdateMachine("build-01", in, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.RevealMachinePassword("build-01"); got != machinePW {
		t.Fatalf("пустой пароль затер прежний: %q", got)
	}
	if list, _ := d.Machines("проект-б"); len(list) != 1 {
		t.Fatalf("правка не дошла: %+v", list)
	}
	if err := d.UpdateMachine("build-01", in, "Новый-Пароль-2!"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.RevealMachinePassword("build-01"); got != "Новый-Пароль-2!" {
		t.Fatalf("новый пароль не лег: %q", got)
	}
}

// Удаление — после Touch ID, снимает и пароль; отказ Touch ID ничего не трогает.
// Удаленный бастион: цепочки через него показывают разрыв, список жив.
func TestMachineDesk_delete(t *testing.T) {
	d, auth, sec := newMachineDesk(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	edge := machineIn{Name: "edge-02", Group: "Прод", Host: "203.0.113.7", User: "ops", Auth: "key"}
	via := buildIn()
	via.Jump = "edge-02"
	for _, step := range []error{d.AddMachine(edge, ""), d.AddMachine(via, machinePW)} {
		if step != nil {
			t.Fatal(step)
		}
	}
	auth.ok = false
	if err := d.DeleteMachine("build-01"); err == nil {
		t.Fatal("удалено без Touch ID")
	}
	if list, _ := d.Machines(""); len(list) != 2 {
		t.Fatalf("отказ Touch ID тронул список: %d", len(list))
	}
	auth.ok = true
	if err := d.DeleteMachine("edge-02"); err != nil {
		t.Fatal(err)
	}
	list, err := d.Machines("")
	if err != nil || len(list) != 1 || list[0].Command != "" || !strings.Contains(list[0].ChainError, "edge-02") {
		t.Fatalf("разрыв цепочки не показан: %+v, %v", list, err)
	}
	if err := d.DeleteMachine("build-01"); err != nil {
		t.Fatal(err)
	}
	if len(sec) != 0 {
		t.Fatalf("пароль остался в Связке: %v", sec)
	}
}

// Машина без проектов уходит в окно как "projects": [], а не null: окно считает
// m.projects.length, и null давал «Машина не сохранена: null is not an object».
func TestMachineDesk_noProjectsIsAnEmptyListNotNull(t *testing.T) {
	d, _, _ := newMachineDesk(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	in := buildIn()
	in.Projects = nil
	if err := d.AddMachine(in, machinePW); err != nil {
		t.Fatal(err)
	}
	list, err := d.Machines("")
	if err != nil || len(list) != 1 {
		t.Fatalf("список: %v %v", list, err)
	}
	raw, err := json.Marshal(list[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"projects":[]`) || strings.Contains(string(raw), `"projects":null`) {
		t.Errorf("projects в JSON: %s", raw)
	}
}
