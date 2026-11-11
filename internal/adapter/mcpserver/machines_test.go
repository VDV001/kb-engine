package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

const (
	machineNote = "пароль-в-заметке-Zx9"
	machineKey  = "~/.ssh/секретный_ключ_ed25519"
	machineHint = "токен выдает портал-Wq4"
)

// stubMachines finds machines; fail — карточки не прочитать (ошибка как у
// настоящего файла: путь с домашним каталогом и кусок содержимого).
type stubMachines struct {
	list []domain.Machine
	fail bool
}

const machinesFailText = "машины: /Users/someone/Library/Application Support/kb-desktop/machines.json испорчен: " + machineNote

func (s stubMachines) Find(q string) ([]domain.Machine, error) {
	if s.fail {
		return nil, errors.New(machinesFailText)
	}
	var out []domain.Machine
	for _, m := range s.list {
		if m.Matches(q) {
			out = append(out, m)
		}
	}
	return out, nil
}

func connectMachines(t *testing.T, f mcpserver.MachineFinder) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: fixture(t)}, search.Matcher{}, mcpserver.Config{Version: "тест", Machines: f})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("сервер не поднялся: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("клиент не подключился: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func stubMachine(t *testing.T) domain.Machine {
	t.Helper()
	m, err := domain.NewMachine(domain.MachineDTO{Name: "build-01", Group: "Сборка", Env: domain.EnvStage, Host: "10.0.0.5", User: "deploy",
		Auth: domain.AuthKey, KeyPath: machineKey, TokenHint: machineHint, Note: machineNote,
		Projects: []domain.MachineProject{{Name: "проект-б", Path: "/srv/b", Port: "8081", What: "сайт"}}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Без машин инструмента нет вовсе; с ними search_machines отдает имя, хост,
// порт, пользователя, способ входа и проекты. Ни пути ключа, ни заметки, ни
// подсказки про токен, ни пароля: все, что получает агент, уходит провайдеру модели.
func TestServer_searchMachines(t *testing.T) {
	if toolNames(t, connect(t, stubQuerier{entries: fixture(t)}))["search_machines"] {
		t.Fatal("search_machines объявлен без машин")
	}
	cs := connectMachines(t, stubMachines{list: []domain.Machine{stubMachine(t)}})
	if !toolNames(t, cs)["search_machines"] {
		t.Fatal("search_machines не объявлен")
	}
	res := callTool(t, cs, "search_machines", map[string]any{"query": "проект-б"})
	if res.IsError {
		t.Fatalf("ошибка: %+v", res.Content)
	}
	raw, _ := json.Marshal(res)
	text := string(raw)
	for _, want := range []string{"build-01", "10.0.0.5", "deploy", "key", "проект-б", "/srv/b", "8081"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	for _, leak := range []string{machineKey, "секретный_ключ", machineNote, "Zx9", machineHint, "Wq4", `"note"`, `"password"`, `"key_path"`, `"token_hint"`} {
		if strings.Contains(text, leak) {
			t.Errorf("утечка %q: %s", leak, text)
		}
	}
	empty, _ := json.Marshal(callTool(t, cs, "search_machines", map[string]any{"query": "проект-я"}))
	if !strings.Contains(string(empty), `found\":0`) {
		t.Errorf("пустая выдача: %s", empty)
	}
}

// Карточки не прочитались — ошибка инструмента, и в ней нет ни пути с домашним
// каталогом, ни содержимого файла.
func TestServer_searchMachines_failure(t *testing.T) {
	cs := connectMachines(t, stubMachines{fail: true})
	res := callTool(t, cs, "search_machines", map[string]any{"query": "build"})
	if !res.IsError {
		t.Fatal("сбой чтения машин не стал ошибкой")
	}
	raw, _ := json.Marshal(res)
	for _, leak := range []string{"/Users/someone", "Zx9", machineNote} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("ошибка отдала агенту %q: %s", leak, raw)
		}
	}
}

// Заметка, путь ключа и подсказка про токен не ищутся: число найденного не
// выдает их содержимое по подстроке.
func TestServer_searchMachines_secretsAreNotSearchable(t *testing.T) {
	cs := connectMachines(t, stubMachines{list: []domain.Machine{stubMachine(t)}})
	for _, q := range []string{"Zx9", "секретный_ключ", "Wq4"} {
		raw, _ := json.Marshal(callTool(t, cs, "search_machines", map[string]any{"query": q}))
		if !strings.Contains(string(raw), `found\":0`) {
			t.Fatalf("фрагмент секрета %q нашелся: %s", q, raw)
		}
	}
}
