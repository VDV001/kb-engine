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

// stubAccess finds cards; fail — карточки не прочитать (ошибка как у
// настоящего файла: путь с домашним каталогом и кусок содержимого).
type stubAccess struct {
	cards []domain.Credential
	fail  bool
}

const failText = "доступы: /Users/someone/Library/Application Support/kb-desktop/credentials.json испорчен: " + notePassword

func (s stubAccess) Find(q string) ([]domain.Credential, error) {
	if s.fail {
		return nil, errors.New(failText)
	}
	var out []domain.Credential
	for _, c := range s.cards {
		if c.Matches(q) {
			out = append(out, c)
		}
	}
	return out, nil
}

func connectAccess(t *testing.T, a mcpserver.AccessFinder) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: fixture(t)}, search.Matcher{}, mcpserver.Config{Version: "тест", Access: a})
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

const notePassword = "пароль-в-заметке-Kq7"

func accessCard(t *testing.T) domain.Credential {
	t.Helper()
	c, err := domain.NewCredential(domain.CredentialDTO{Product: "Склад-Про", Env: domain.EnvProd, URL: "https://sklad.example.com",
		Host: "192.0.2.14", Login: "admin.ops", Role: "администратор", Rights: "все разделы", IssuedBy: "тимлид", Note: notePassword})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Без доступов инструмента нет вовсе; с ними search_access отдает карточку:
// продукт, среда, адрес, логин, роль — и подсказку, как взять пароль самому.
// Ни пароля, ни заметки (туда пишут что угодно, в том числе пароль): все,
// что получает агент, уходит провайдеру модели.
func TestServer_searchAccess(t *testing.T) {
	if toolNames(t, connect(t, stubQuerier{entries: fixture(t)}))["search_access"] {
		t.Fatal("search_access объявлен без доступов")
	}
	cs := connectAccess(t, stubAccess{cards: []domain.Credential{accessCard(t)}})
	if !toolNames(t, cs)["search_access"] {
		t.Fatal("search_access не объявлен")
	}
	res := callTool(t, cs, "search_access", map[string]any{"query": "склад прод"})
	if res.IsError {
		t.Fatalf("ошибка: %+v", res.Content)
	}
	raw, _ := json.Marshal(res)
	text := string(raw)
	for _, want := range []string{"sklad-pro-prod-admin-ops", "Склад-Про", "prod", "https://sklad.example.com", "admin.ops", "администратор", "kbengine creds copy sklad-pro-prod-admin-ops"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	for _, leak := range []string{notePassword, "Kq7", `"note"`, `"password"`} {
		if strings.Contains(text, leak) {
			t.Errorf("утечка %q: %s", leak, text)
		}
	}
	empty, _ := json.Marshal(callTool(t, cs, "search_access", map[string]any{"query": "портал"}))
	if !strings.Contains(string(empty), `found\":0`) { // текст ответа — компактный JSON внутри JSON
		t.Errorf("пустая выдача: %s", empty)
	}
}

// Карточки не прочитались — ошибка инструмента, и в ней тоже нет заметки.
func TestServer_searchAccess_failure(t *testing.T) {
	cs := connectAccess(t, stubAccess{fail: true})
	res := callTool(t, cs, "search_access", map[string]any{"query": "склад"})
	if !res.IsError {
		t.Fatal("сбой чтения карточек не стал ошибкой")
	}
	raw, _ := json.Marshal(res)
	for _, leak := range []string{"/Users/someone", "Kq7", notePassword} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("ошибка отдала агенту %q: %s", leak, raw)
		}
	}
}

// Заметка не ищется: число найденного не выдает ее содержимое по подстроке.
func TestServer_searchAccess_noteIsNotSearchable(t *testing.T) {
	cs := connectAccess(t, stubAccess{cards: []domain.Credential{accessCard(t)}})
	raw, _ := json.Marshal(callTool(t, cs, "search_access", map[string]any{"query": "Kq7"}))
	if !strings.Contains(string(raw), `found\":0`) {
		t.Fatalf("фрагмент заметки нашелся: %s", raw)
	}
}
