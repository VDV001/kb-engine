package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect поднимает настоящий сервер и настоящий клиент SDK на транспорте в
// памяти. Протокол проверяется целиком, а не мокается: рукопожатие, tools/list
// и tools/call — ровно то, что сделает Claude Code, только без процесса.
func connect(t *testing.T, q mcpserver.Querier) *mcp.ClientSession {
	t.Helper()
	return connectWithView(t, q, "")
}

// connectWithView — тот же сервер, но с объявленной витриной: поле view
// проверяется отдельно, чтобы остальные тесты не зависели от её адреса.
func connectWithView(t *testing.T, q mcpserver.Querier, viewBase string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(q, search.Matcher{}, "тест", viewBase)
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

func TestServer_advertisesItsTools(t *testing.T) {
	cs := connect(t, stubQuerier{entries: fixture(t)})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"search_catalog", "get_entry", "stats"} {
		if !got[want] {
			t.Fatalf("инструмент %q не объявлен, объявлены: %v", want, got)
		}
	}
}

// Тот же вопрос, что задаёт агент, и тот же ответ, что у usecase — иначе
// завелась бы третья копия правила поиска (#252 этажом выше).
func TestServer_searchGoesThroughTheProtocol(t *testing.T) {
	cs := connect(t, stubQuerier{entries: fixture(t)})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_catalog", Arguments: map[string]any{"query": "кубернетес"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("вызов вернул ошибку: %s", text(t, res))
	}
	var out struct {
		Found   int `json:"found"`
		Entries []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &out); err != nil {
		t.Fatalf("ответ не разбирается как JSON: %v\n%s", err, text(t, res))
	}
	if out.Found != 1 || len(out.Entries) != 1 || out.Entries[0].ID != 1 {
		t.Fatalf("ожидалась одна запись #1, отдано: %+v", out)
	}
}

// Отрицательный контроль приёмки: запрос, которого в базе нет, отвечает пусто
// и БЕЗ признака ошибки — «ничего не нашлось» и «сломалось» это разные ответы.
func TestServer_unknownQueryIsEmptyNotError(t *testing.T) {
	cs := connect(t, stubQuerier{entries: fixture(t)})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_catalog", Arguments: map[string]any{"query": "телепортация"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("пустая выдача не должна быть ошибкой: %s", text(t, res))
	}
	var out struct {
		Found int `json:"found"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &out); err != nil {
		t.Fatalf("ответ не разбирается: %v", err)
	}
	if out.Found != 0 {
		t.Fatalf("ожидалось found:0, отдано %d", out.Found)
	}
}

// Выдуманный id — ошибка протокола, а не чужая карточка.
func TestServer_unknownIDIsProtocolError(t *testing.T) {
	cs := connect(t, stubQuerier{entries: fixture(t)})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_entry", Arguments: map[string]any{"id": 9999},
	})
	if err != nil {
		return // отказ на уровне протокола — тоже законный ответ
	}
	if !res.IsError {
		t.Fatalf("выдуманный id обязан быть ошибкой, отдано: %s", text(t, res))
	}
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Поле view проверяется НА ЖИВОМ ПУТИ протокола, а не на функции: адрес может
// собираться верно и не доезжать до ответа — так уже было в deal-sense, где
// разбивка часов считалась и выбрасывалась обработчиком.
func TestServer_searchCarriesTheViewURL(t *testing.T) {
	cs := connectWithView(t, stubQuerier{entries: fixture(t)}, "http://127.0.0.1:8097")
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_catalog", Arguments: map[string]any{"query": "кубернетес"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		View string `json:"view"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &got); err != nil {
		t.Fatal(err)
	}
	const want = "http://127.0.0.1:8097/?tab=archives&q=%D0%BA%D1%83%D0%B1%D0%B5%D1%80%D0%BD%D0%B5%D1%82%D0%B5%D1%81"
	if got.View != want {
		t.Errorf("view = %q, want %q", got.View, want)
	}
}

// Отрицательный контроль: без объявленной витрины адреса нет вовсе. Ссылка в
// никуда хуже её отсутствия — по ней сходят один раз и перестают верить полю.
func TestServer_withoutViewBaseTheFieldStaysEmpty(t *testing.T) {
	cs := connect(t, stubQuerier{entries: fixture(t)})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_catalog", Arguments: map[string]any{"query": "кубернетес"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		View string `json:"view"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &got); err != nil {
		t.Fatal(err)
	}
	if got.View != "" {
		t.Errorf("view = %q, ждали пустое: витрина не объявлена", got.View)
	}
}
