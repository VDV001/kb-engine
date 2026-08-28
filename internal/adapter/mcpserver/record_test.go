package mcpserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// call — один записанный вызов так, как его видит журнал.
type call struct {
	tool     string
	args     []string
	took     time.Duration
	exitCode int
}

// recorderStub — журнал в памяти. Ошибок не возвращает намеренно: порт их не
// имеет, решение о сломанном журнале принимает точка сборки.
type recorderStub struct{ calls []call }

func (r *recorderStub) RecordCall(tool string, args []string, _ time.Time, took time.Duration, exitCode int) {
	r.calls = append(r.calls, call{tool: tool, args: args, took: took, exitCode: exitCode})
}

func connectWithRecorder(t *testing.T, q mcpserver.Querier, rec mcpserver.Recorder) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(q, search.Matcher{}, mcpserver.Config{Version: "тест", Recorder: rec})
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

// Каждый инструмент оставляет след — проверяется на ЖИВОМ пути протокола, а не
// на функции записи: до этого счётчика не было вовсе, и вопрос «окупились ли
// ссылки на витрину» оставался мнением.
//
// Проверяются все три инструмента разом: запись, приделанная к каждому вручную,
// однажды забудется у четвёртого.
func TestServer_recordsEveryToolCall(t *testing.T) {
	rec := &recorderStub{}
	cs := connectWithRecorder(t, stubQuerier{entries: fixture(t)}, rec)
	ctx := context.Background()

	for _, p := range []*mcp.CallToolParams{
		{Name: "search_catalog", Arguments: map[string]any{"query": "кубернетес"}},
		{Name: "get_entry", Arguments: map[string]any{"id": 1}},
		{Name: "stats"},
	} {
		if _, err := cs.CallTool(ctx, p); err != nil {
			t.Fatalf("вызов %s: %v", p.Name, err)
		}
	}

	if len(rec.calls) != 3 {
		t.Fatalf("записано вызовов %d, ждали 3: %+v", len(rec.calls), rec.calls)
	}
	want := []string{"search_catalog", "get_entry", "stats"}
	for i, w := range want {
		if rec.calls[i].tool != w {
			t.Errorf("вызов %d записан как %q, ждали %q", i, rec.calls[i].tool, w)
		}
		if rec.calls[i].exitCode != 0 {
			t.Errorf("вызов %q записан с кодом %d, ждали 0", w, rec.calls[i].exitCode)
		}
	}
	// Запрос записывается: без него счётчик отвечает «сколько», но не «о чём», а
	// вкладка «Ответы» строится именно на этом.
	if len(rec.calls[0].args) != 1 || rec.calls[0].args[0] != "кубернетес" {
		t.Errorf("аргументы поиска = %v, ждали [кубернетес]", rec.calls[0].args)
	}
}

// Отрицательный контроль: неудавшийся вызов записывается ОТДЕЛЬНЫМ кодом.
// Иначе «спросили и не получили ответа» выглядело бы в счётчике как удачный
// вызов — то же самое, что показывать зелёным проверку, которая не выполнилась.
func TestServer_recordsFailedCallWithNonZeroCode(t *testing.T) {
	rec := &recorderStub{}
	cs := connectWithRecorder(t, stubQuerier{entries: fixture(t)}, rec)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_entry", Arguments: map[string]any{"id": 9999},
	}); err != nil {
		t.Logf("отказ на уровне протокола: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("записано вызовов %d, ждали 1", len(rec.calls))
	}
	if rec.calls[0].exitCode == 0 {
		t.Errorf("промах по id записан с кодом 0 — в счётчике он неотличим от удачного вызова")
	}
}

// Журнала может не быть вовсе: сервер поднимают и без него. Это законное
// состояние, а не поломка, и отвечать на вызовы он обязан по-прежнему.
func TestServer_answersWithoutRecorder(t *testing.T) {
	cs := connectWithRecorder(t, stubQuerier{entries: fixture(t)}, nil)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_catalog", Arguments: map[string]any{"query": "кубернетес"},
	})
	if err != nil {
		t.Fatalf("вызов без журнала обязан работать: %v", err)
	}
	if res.IsError {
		t.Fatalf("вызов без журнала вернул ошибку: %s", text(t, res))
	}
}
