package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubFiles — индекс файлов в памяти. Настоящий Service обходит диск, а вопрос
// этих тестов в том, что MCP переводит вызов в тот же порт и честно называет
// его отказы.
type stubFiles struct {
	hits      []filesearch.Hit
	searchErr error
	texts     map[string]string
	readErr   error
	lastLimit int
}

func (s *stubFiles) Search(_ string, limit int) ([]filesearch.Hit, error) {
	s.lastLimit = limit
	return s.hits, s.searchErr
}

func (s *stubFiles) Read(path string) (string, error) {
	if s.readErr != nil {
		return "", s.readErr
	}
	t, ok := s.texts[path]
	if !ok {
		return "", errors.New("open " + path + ": no such file or directory")
	}
	return t, nil
}

func (s *stubFiles) Stats() filesearch.Stats { return filesearch.Stats{Indexed: 42, Skipped: 1} }

func connectFiles(t *testing.T, f mcpserver.FileSearcher) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: fixture(t)}, search.Matcher{}, mcpserver.Config{Version: "тест", Files: f})
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

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	return got
}

func fileDoc(t *testing.T, path string) domain.FileDoc {
	t.Helper()
	d, err := domain.NewFileDoc(path, 120, time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestServer_fileToolsAppearOnlyWithFiles(t *testing.T) {
	// Сервер над одним каталогом не делает вид, что умеет искать по диску:
	// без индекса файлов инструментов нет вовсе, как маршрутов /api/files/*.
	without := toolNames(t, connect(t, stubQuerier{entries: fixture(t)}))
	with := toolNames(t, connectFiles(t, &stubFiles{}))
	for _, name := range []string{"search_files", "read_file"} {
		if without[name] {
			t.Errorf("%s объявлен без индекса файлов", name)
		}
		if !with[name] {
			t.Errorf("%s не объявлен при индексе файлов: %v", name, with)
		}
	}
}

func TestServer_searchFiles(t *testing.T) {
	f := &stubFiles{hits: []filesearch.Hit{{Doc: fileDoc(t, "/notes/mcp.md"), Score: 2.5, Snippet: "разбор MCP"}}}
	res := callTool(t, connectFiles(t, f), "search_files", map[string]any{"query": "mcp", "limit": 5})
	if res.IsError {
		t.Fatalf("ошибка: %s", text(t, res))
	}
	var out struct {
		Indexed int `json:"indexed"`
		Found   int `json:"found"`
		Hits    []struct {
			Path     string `json:"path"`
			Name     string `json:"name"`
			Modified string `json:"modified"`
			Snippet  string `json:"snippet"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &out); err != nil {
		t.Fatalf("не JSON: %v\n%s", err, text(t, res))
	}
	// Размер индекса идет вместе с выдачей: «не нашлось» без него не отличить
	// от «не искали».
	if out.Indexed != 42 || out.Found != 1 || len(out.Hits) != 1 {
		t.Fatalf("out = %+v", out)
	}
	h := out.Hits[0]
	if h.Path != "/notes/mcp.md" || h.Name != "mcp.md" || h.Snippet != "разбор MCP" || h.Modified != "2026-11-03T10:00:00Z" {
		t.Fatalf("hit = %+v", h)
	}
	if f.lastLimit != 5 {
		t.Errorf("limit = %d, want 5", f.lastLimit)
	}
}

func TestServer_searchFilesLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit any
		want  int
	}{
		{"не задан — умолчание", nil, 20},
		{"ноль — умолчание", 0, 20},
		{"больше потолка — потолок", 100000, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &stubFiles{}
			args := map[string]any{"query": "mcp"}
			if tt.limit != nil {
				args["limit"] = tt.limit
			}
			if res := callTool(t, connectFiles(t, f), "search_files", args); res.IsError {
				t.Fatalf("ошибка: %s", text(t, res))
			}
			if f.lastLimit != tt.want {
				t.Errorf("limit = %d, want %d", f.lastLimit, tt.want)
			}
		})
	}
}

func TestServer_searchFilesErrors(t *testing.T) {
	tests := []struct {
		name  string
		files *stubFiles
		query string
		want  string
	}{
		{"пустой запрос", &stubFiles{}, "", "запрос"},
		{"индекс собирается", &stubFiles{searchErr: filesearch.ErrNotBuilt}, "mcp", "собирается"},
		// Упавшая сборка называется по-русски и с тем, что делать, а сырой
		// путь из ошибки обхода к агенту не уходит (ревью #375).
		{"сборка упала", &stubFiles{searchErr: fmt.Errorf("%w: walk files: open /Users/someone/Documents: operation not permitted", filesearch.ErrBuildFailed)}, "mcp", "не собрался"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := callTool(t, connectFiles(t, tt.files), "search_files", map[string]any{"query": tt.query})
			if !res.IsError || !strings.Contains(text(t, res), tt.want) {
				t.Fatalf("ждали ошибку со словом %q, отдано: error=%v %s", tt.want, res.IsError, text(t, res))
			}
			if strings.Contains(text(t, res), "/Users/someone") {
				t.Fatalf("ответ раскрывает путь: %s", text(t, res))
			}
		})
	}
}

func TestServer_readFileKeepsMarkupLiteral(t *testing.T) {
	// HTML-экранирование encoding/json превращает каждый «<» в шесть символов
	// \u003c: кусок кода или разметки раздувался вдвое и выходил за порог
	// вывода MCP, рассчитанный по рунам (ревью #375).
	f := &stubFiles{texts: map[string]string{"/a.tsx": "<a href=\"x\">a & b</a>"}}
	got := text(t, callTool(t, connectFiles(t, f), "read_file", map[string]any{"path": "/a.tsx"}))
	if strings.Contains(got, `\u003c`) || !strings.Contains(got, "a & b</a>") {
		t.Fatalf("разметка экранирована: %s", got)
	}
}

func TestServer_readFile(t *testing.T) {
	f := &stubFiles{texts: map[string]string{"/notes/mcp.md": "# MCP\nпротокол"}}
	res := callTool(t, connectFiles(t, f), "read_file", map[string]any{"path": "/notes/mcp.md"})
	if res.IsError {
		t.Fatalf("ошибка: %s", text(t, res))
	}
	var out struct {
		Path       string `json:"path"`
		Text       string `json:"text"`
		TotalRunes int    `json:"total_runes"`
		NextOffset int    `json:"next_offset"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Path != "/notes/mcp.md" || out.Text != "# MCP\nпротокол" || out.TotalRunes != 14 || out.NextOffset != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestServer_readFileInChunks(t *testing.T) {
	// Файл до 2 МБ целиком не влезает в ответ инструмента: клиент режет вывод
	// MCP по токенам. Длинный файл отдается кусками по рунам, кириллица не
	// рвется посреди символа, и последний кусок не обещает продолжения.
	long := strings.Repeat("ж", mcpserver.ReadChunkRunes+10)
	f := &stubFiles{texts: map[string]string{"/notes/long.md": long}}
	cs := connectFiles(t, f)

	var first, second struct {
		Text       string `json:"text"`
		TotalRunes int    `json:"total_runes"`
		NextOffset int    `json:"next_offset"`
	}
	if err := json.Unmarshal([]byte(text(t, callTool(t, cs, "read_file", map[string]any{"path": "/notes/long.md"}))), &first); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(first.Text)); n != mcpserver.ReadChunkRunes || first.NextOffset != mcpserver.ReadChunkRunes || first.TotalRunes != mcpserver.ReadChunkRunes+10 {
		t.Fatalf("первый кусок: %d рун, next=%d, total=%d", n, first.NextOffset, first.TotalRunes)
	}
	res := callTool(t, cs, "read_file", map[string]any{"path": "/notes/long.md", "offset": first.NextOffset})
	if err := json.Unmarshal([]byte(text(t, res)), &second); err != nil {
		t.Fatal(err)
	}
	if second.Text != strings.Repeat("ж", 10) || second.NextOffset != 0 {
		t.Fatalf("второй кусок: %q next=%d", second.Text, second.NextOffset)
	}
}

func TestServer_readFileErrors(t *testing.T) {
	tests := []struct {
		name  string
		files *stubFiles
		args  map[string]any
		want  string
		leak  string // чего в ответе быть не должно
	}{
		{"пустой путь", &stubFiles{}, map[string]any{"path": ""}, "путь", ""},
		{"вне папок или секрет", &stubFiles{readErr: filesearch.ErrNotAllowed}, map[string]any{"path": "/etc/passwd"}, "не разрешен", ""},
		// Причина не раскрывается: «нет файла» и «нет прав» по любому пути
		// были бы способом простучать диск — как в HTTP API.
		{"нет файла", &stubFiles{}, map[string]any{"path": "/notes/gone.md"}, "не найден", "no such file"},
		{"смещение за концом", &stubFiles{texts: map[string]string{"/a.md": "abc"}}, map[string]any{"path": "/a.md", "offset": 4}, "смещение", ""},
		{"отрицательное смещение", &stubFiles{texts: map[string]string{"/a.md": "abc"}}, map[string]any{"path": "/a.md", "offset": -1}, "смещение", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ошибка протокола здесь — провал, а не законный ответ: иначе случай,
			// отвергнутый схемой, проходил бы пустым и ветка отказа оставалась
			// непроверенной (ревью #375).
			res := callTool(t, connectFiles(t, tt.files), "read_file", tt.args)
			got := text(t, res)
			if !res.IsError || !strings.Contains(got, tt.want) {
				t.Fatalf("ждали ошибку со словом %q, отдано: error=%v %s", tt.want, res.IsError, got)
			}
			if tt.leak != "" && strings.Contains(got, tt.leak) {
				t.Fatalf("ответ раскрывает причину %q: %s", tt.leak, got)
			}
		})
	}
}
