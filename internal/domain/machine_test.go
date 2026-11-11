package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Адреса из блоков документации (RFC 5737), имена и пути выдуманные.
func machineDTO() domain.MachineDTO {
	return domain.MachineDTO{
		Name: " build-01 ", Group: "Сборка", Env: domain.EnvStage, Host: "10.0.0.5", User: "deploy",
		Auth: domain.AuthKey, KeyPath: "~/.ssh/build_ed25519", Note: "перезагрузка по субботам",
		Projects: []domain.MachineProject{
			{Name: "проект-а", Path: "/srv/a", Port: "8080", What: "api и очередь"},
			{Name: "проект-б", Path: "/srv/b", Port: "8081", What: "сайт"},
		},
	}
}

// Машина — не учетка: хост и пользователь обязательны, среда нет; порт по
// умолчанию 22; id берется из имени латиницей.
func TestNewMachine(t *testing.T) {
	m, err := domain.NewMachine(machineDTO())
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	if m.ID() != "build-01" || m.Name() != "build-01" {
		t.Errorf("имя и id: %q, %q", m.Name(), m.ID())
	}
	if m.Port() != 22 {
		t.Errorf("порт по умолчанию = %d, нужно 22", m.Port())
	}
	if len(m.Projects()) != 2 {
		t.Errorf("проектов %d, нужно 2", len(m.Projects()))
	}

	noEnv := machineDTO()
	noEnv.Env = ""
	if _, err := domain.NewMachine(noEnv); err != nil {
		t.Errorf("среда у машины не обязательна: %v", err)
	}
}

func TestNewMachine_rejects(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(*domain.MachineDTO)
	}{
		{"нет имени", func(d *domain.MachineDTO) { d.Name = " " }},
		{"нет хоста", func(d *domain.MachineDTO) { d.Host = "" }},
		{"нет пользователя", func(d *domain.MachineDTO) { d.User = "" }},
		{"порт ноль", func(d *domain.MachineDTO) { d.Port = -1 }},
		{"порт больше 65535", func(d *domain.MachineDTO) { d.Port = 70000 }},
		{"незнакомый способ входа", func(d *domain.MachineDTO) { d.Auth = "telepathy" }},
		{"проект без имени", func(d *domain.MachineDTO) { d.Projects[0].Name = "" }},
		{"незнакомая среда", func(d *domain.MachineDTO) { d.Env = "qa-2" }},
		{"кривой id", func(d *domain.MachineDTO) { d.ID = "Build 01!" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := machineDTO()
			c.mut(&d)
			if _, err := domain.NewMachine(d); !errors.Is(err, domain.ErrInvalidMachine) {
				t.Errorf("принято: %v", err)
			}
		})
	}
}

// Поиск находит машину по проекту, пути и пользователю. Заметка, путь ключа и
// подсказка про токен поиску не видны: все, что находит поиск, видит агент и
// уходит провайдеру модели (как у доступов).
func TestMachine_Matches(t *testing.T) {
	d := machineDTO()
	d.TokenHint = "выдается порталом на 8 часов"
	m, err := domain.NewMachine(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q    string
		want bool
	}{
		{"", true},
		{"проект-б", true},
		{"/srv/a", true},
		{"deploy", true},
		{"10.0.0.5", true},
		{"сборка стенд", true},
		{"проект-я", false},
		{"суббота", false},           // заметка
		{"build_ed25519", false},     // путь ключа
		{"портал", false},            // подсказка про токен
		{"проект-а проект-я", false}, // каждое слово должно найтись
	} {
		if got := m.Matches(c.q); got != c.want {
			t.Errorf("Matches(%q) = %v, нужно %v", c.q, got, c.want)
		}
	}
}

func bastions(t *testing.T) (target, edge, deep domain.Machine, byID func(string) (domain.Machine, bool)) {
	t.Helper()
	edgeDTO := domain.MachineDTO{Name: "edge-02", Host: "203.0.113.7", Port: 2222, User: "ops", Auth: domain.AuthPassword}
	deepDTO := domain.MachineDTO{Name: "inner-04", Host: "10.0.1.4", User: "ops", Auth: domain.AuthKey, Jump: "edge-02"}
	tgtDTO := machineDTO()
	tgtDTO.Jump = "inner-04"
	var err error
	if edge, err = domain.NewMachine(edgeDTO); err != nil {
		t.Fatal(err)
	}
	if deep, err = domain.NewMachine(deepDTO); err != nil {
		t.Fatal(err)
	}
	if target, err = domain.NewMachine(tgtDTO); err != nil {
		t.Fatal(err)
	}
	all := map[string]domain.Machine{edge.ID(): edge, deep.ID(): deep, target.ID(): target}
	return target, edge, deep, func(id string) (domain.Machine, bool) { m, ok := all[id]; return m, ok }
}

// Цепочка собирается из своих же карточек: от внешнего бастиона к цели.
func TestResolveChain(t *testing.T) {
	target, edge, deep, byID := bastions(t)
	chain, err := domain.ResolveChain(target, byID)
	if err != nil {
		t.Fatalf("ResolveChain: %v", err)
	}
	if len(chain) != 3 || chain[0].ID() != edge.ID() || chain[1].ID() != deep.ID() || chain[2].ID() != target.ID() {
		t.Errorf("цепочка не от бастиона к цели: %v", ids(chain))
	}

	// Удалили бастион: разрыв называется, а не молча ломает команду.
	_, err = domain.ResolveChain(target, func(id string) (domain.Machine, bool) {
		if id == "edge-02" {
			return domain.Machine{}, false
		}
		return byID(id)
	})
	if !errors.Is(err, domain.ErrMachineChain) || !strings.Contains(err.Error(), "edge-02") {
		t.Errorf("разрыв цепочки не назван: %v", err)
	}

	// Бастион ссылается на цель: петля, а не бесконечный обход.
	loop := edge.DTO()
	loop.Jump = target.ID()
	edgeLoop, err := domain.NewMachine(loop)
	if err != nil {
		t.Fatal(err)
	}
	_, err = domain.ResolveChain(target, func(id string) (domain.Machine, bool) {
		if id == "edge-02" {
			return edgeLoop, true
		}
		return byID(id)
	})
	if !errors.Is(err, domain.ErrMachineChain) {
		t.Errorf("петля не поймана: %v", err)
	}
}

// Команда ssh из цепочки: -J для бастионов, -i для ключа, порт и пользователь цели.
func TestMachine_SSHCommand(t *testing.T) {
	target, _, _, byID := bastions(t)
	chain, err := domain.ResolveChain(target, byID)
	if err != nil {
		t.Fatal(err)
	}
	want := "ssh -J ops@203.0.113.7:2222,ops@10.0.1.4:22 -i ~/.ssh/build_ed25519 -p 22 deploy@10.0.0.5"
	if got := domain.SSHCommand(chain); got != want {
		t.Errorf("команда:\n got %s\nwant %s", got, want)
	}

	solo, err := domain.NewMachine(domain.MachineDTO{Name: "solo", Host: "198.51.100.9", Port: 2200, User: "root", Auth: domain.AuthPassword})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := domain.SSHCommand([]domain.Machine{solo}), "ssh -p 2200 root@198.51.100.9"; got != want {
		t.Errorf("команда без бастиона:\n got %s\nwant %s", got, want)
	}

	// Путь ключа с пробелом не должен рассыпать команду при копировании; тильда
	// вне кавычек, иначе оболочка ее не раскроет.
	spaced := machineDTO()
	spaced.KeyPath = "~/My Keys/id"
	m, err := domain.NewMachine(spaced)
	if err != nil {
		t.Fatal(err)
	}
	if got := domain.SSHCommand([]domain.Machine{m}); !strings.Contains(got, "-i ~/'My Keys/id'") {
		t.Errorf("путь с пробелом не взят в кавычки: %s", got)
	}
}

// Пароль, путь ключа и заметка не попадают в то, что получает агент.
func TestMachine_AgentView(t *testing.T) {
	d := machineDTO()
	d.TokenHint = "выдается порталом"
	m, err := domain.NewMachine(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m.AgentView())
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, secret := range []string{d.KeyPath, d.Note, d.TokenHint} {
		if strings.Contains(got, secret) {
			t.Errorf("в выдаче агенту есть %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "10.0.0.5") || !strings.Contains(got, "проект-б") {
		t.Errorf("в выдаче нет хоста или проектов: %s", got)
	}
}

func ids(ms []domain.Machine) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID()
	}
	return out
}
