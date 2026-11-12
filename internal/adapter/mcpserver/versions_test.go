package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/daniil/kb-engine/internal/usecase/versions"
)

type stubVersions struct {
	fams []mcpserver.VersionFamily
	text map[string]string
	fail bool
	err  error // причина сбоя; пусто и fail — текст с путем и содержимым
	read []string
}

const versionsFailText = "обход: /Users/someone/Documents/КП.docx не открылся: SECRET-в-тексте"

func (s *stubVersions) Families(_ context.Context, q string) ([]mcpserver.VersionFamily, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.fail {
		return nil, errors.New(versionsFailText)
	}
	var out []mcpserver.VersionFamily
	for _, f := range s.fams {
		if q == "" || strings.Contains(strings.ToLower(f.Name), strings.ToLower(q)) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *stubVersions) Text(_ context.Context, path string) (string, error) {
	if s.fail {
		return "", errors.New(versionsFailText)
	}
	s.read = append(s.read, path)
	return s.text[path], nil
}

func kpStub() *stubVersions {
	return &stubVersions{
		fams: []mcpserver.VersionFamily{{Name: "КП Ромашка", Versions: []mcpserver.VersionEntry{
			{Path: "/p/КП_2026_11_05.docx", Date: "05.11.2026", Size: 1000, Note: "первая версия", Mark: "отправлено Алексею"},
			{Path: "/p/КП_2026_11_10.docx", Date: "10.11.2026", Size: 1100, Note: "добавлено строк: 1 — «Гарантия»", Current: true},
		}}, {Name: "Смета", Versions: []mcpserver.VersionEntry{{Path: "/p/Смета.xlsx", Date: "01.11.2026", Note: "первая версия"}}}},
		text: map[string]string{"/p/КП_2026_11_05.docx": "ТЕКСТ-ПЕРВОЙ", "/p/КП_2026_11_10.docx": "ТЕКСТ-ВТОРОЙ"},
	}
}

func connectVersions(t *testing.T, f mcpserver.VersionFinder) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: fixture(t)}, search.Matcher{}, mcpserver.Config{Version: "тест", Versions: f})
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

func asText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	raw, _ := json.Marshal(res)
	return string(raw)
}

// Без версий инструментов нет вовсе; с ними search_versions отдает ленту с
// подписями и заметками, но не текст файлов.
func TestServer_searchVersions(t *testing.T) {
	names := toolNames(t, connect(t, stubQuerier{entries: fixture(t)}))
	if names["search_versions"] || names["open_version"] {
		t.Fatal("инструменты версий объявлены без версий")
	}
	cs := connectVersions(t, kpStub())
	names = toolNames(t, cs)
	if !names["search_versions"] || !names["open_version"] {
		t.Fatalf("инструменты версий не объявлены: %v", names)
	}
	res := callTool(t, cs, "search_versions", map[string]any{"query": "кп"})
	if res.IsError {
		t.Fatalf("ошибка: %+v", res.Content)
	}
	text := asText(t, res)
	for _, want := range []string{"КП Ромашка", "05.11.2026", "первая версия", "отправлено Алексею", "Гарантия", "/p/КП_2026_11_10.docx", "current"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	if strings.Contains(text, "ТЕКСТ-") || strings.Contains(text, "Смета") {
		t.Errorf("лишнее: текст файла или чужой документ: %s", text)
	}
}

func TestServer_openVersion(t *testing.T) {
	for _, c := range []struct{ which, want string }{
		{"последняя", "ТЕКСТ-ВТОРОЙ"}, {"первая", "ТЕКСТ-ПЕРВОЙ"}, {"от 05.11", "ТЕКСТ-ПЕРВОЙ"}, {"актуальная", "ТЕКСТ-ВТОРОЙ"}, {"отправленная Алексею", "ТЕКСТ-ПЕРВОЙ"},
	} {
		stub := kpStub()
		cs := connectVersions(t, stub)
		res := callTool(t, cs, "open_version", map[string]any{"query": "кп", "which": c.which})
		if res.IsError || !strings.Contains(asText(t, res), c.want) || len(stub.read) != 1 {
			t.Errorf("«%s»: ждали %s, читалось %v: %s", c.which, c.want, stub.read, asText(t, res))
		}
	}
}

// Неоднозначный выбор не читает файл: агент получает кандидатов и должен
// спросить человека.
func TestServer_openVersion_asksInsteadOfGuessing(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"нет такой версии":     {"query": "кп", "which": "v9"},
		"нет такого документа": {"query": "договор", "which": "последняя"},
		"два документа":        {"query": "", "which": "последняя"},
		"не сказано какая":     {"query": "кп", "which": ""},
	} {
		stub := kpStub()
		res := callTool(t, connectVersions(t, stub), "open_version", args)
		text := asText(t, res)
		if res.IsError || len(stub.read) != 0 || strings.Contains(text, "ТЕКСТ-") || !strings.Contains(text, "ask") {
			t.Errorf("%s: файл прочитан или нет вопроса к человеку (читалось %v): %s", name, stub.read, text)
		}
	}
}

// Сбой чтения — ошибка инструмента без пути с домашним каталогом и без куска
// текста: все, что получает агент, уходит провайдеру модели.
func TestServer_versions_failureLeaksNothing(t *testing.T) {
	cs := connectVersions(t, &stubVersions{fail: true})
	for _, c := range []struct {
		tool string
		args map[string]any
	}{{"search_versions", map[string]any{"query": "кп"}}, {"open_version", map[string]any{"query": "кп", "which": "последняя"}}} {
		res := callTool(t, cs, c.tool, c.args)
		if !res.IsError {
			t.Fatalf("%s: сбой не стал ошибкой", c.tool)
		}
		for _, leak := range []string{"/Users/someone", "SECRET"} {
			if strings.Contains(asText(t, res), leak) {
				t.Errorf("%s: ошибка отдала агенту %q", c.tool, leak)
			}
		}
	}
}

// Причина сбоя названа словами, но без текста ошибки: «папки не подключены» и
// «запрос отменен» агенту полезны, путь с домашним каталогом — нет.
func TestServer_versions_failureNamesTheCause(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"папки не подключены": {fmt.Errorf("обход: %w", versions.ErrNoSource), "папки окна не подключены"},
		"отменено":            {context.Canceled, "отменен"},
	} {
		cs := connectVersions(t, &stubVersions{err: c.err})
		res := callTool(t, cs, "search_versions", map[string]any{"query": "кп"})
		if !res.IsError || !strings.Contains(asText(t, res), c.want) {
			t.Errorf("%s: %s", name, asText(t, res))
		}
	}
}
