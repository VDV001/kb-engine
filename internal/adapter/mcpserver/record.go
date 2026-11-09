package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Recorder — порт журнала вызовов: кто и о чем спросил каталог.
//
// Метод не возвращает ошибку намеренно. Журнал — наблюдатель, а не участник:
// его поломка не имеет права изменить ответ агенту, а решать, что делать со
// сломанным журналом, обязана точка сборки — у нее есть stderr, которого у
// адаптера нет (и печать из internal/ запрещена гейтом arch #5).
type Recorder interface {
	RecordCall(tool string, args []string, startedAt time.Time, took time.Duration, exitCode int)
}

// Config — то, что сервер узнает снаружи.
//
// Структурой, а не четырьмя позиционными аргументами: две соседние строки
// (версия и адрес витрины) переставляются местами молча, и такой вызов
// собирается.
type Config struct {
	Version  string
	ViewBase string
	// Recorder нулевой означает «журнала нет» — законное состояние, а не
	// поломка: сервер поднимают и без него, и отвечать он обязан по-прежнему.
	Recorder Recorder
	// Now нулевой означает системные часы. Вынесены наружу ради проверяемой
	// длительности, а не ради стиля.
	Now func() time.Time
	// Files нулевой означает «поиска по файлам нет»: инструменты search_files
	// и read_file тогда не объявляются вовсе, как маршруты /api/files/* у HTTP.
	Files FileSearcher
	// Remote нулевой означает «kb-серверов нет»: search_catalog отвечает своим
	// каталогом, как прежде.
	Remote RemoteSearcher
	// Access нулевой означает «доступов нет»: search_access не объявляется.
	Access AccessFinder
}

// journal — счетчик вызовов, приделанный к регистрации инструментов.
type journal struct {
	rec Recorder
	now func() time.Time
}

func newJournal(cfg Config) journal {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return journal{rec: cfg.Recorder, now: now}
}

// call записывает состоявшийся вызов.
//
// Код возврата повторяет договор журнала прогонов: 0 — ответили, 1 — нет.
// Отказ обязан отличаться от успеха, иначе «спросили и не получили ответа»
// выглядит в счетчике как удачный вызов — та же форма 9, что у проверки,
// которая не смогла выполниться и потому промолчала.
func (j journal) call(tool string, args []string, startedAt time.Time, err error) {
	if j.rec == nil {
		return
	}
	code := 0
	if err != nil {
		code = 1
	}
	j.rec.RecordCall(tool, args, startedAt, j.now().Sub(startedAt), code)
}

// addTool регистрирует инструмент так, что запись вызова нельзя забыть.
//
// Журнал приделан к РЕГИСТРАЦИИ, а не к телу обработчика — тот же прием, что у
// runLogged вокруг единственного диспетчера команд: новый инструмент попадает в
// счетчик тем, что он объявлен, а не тем, что автор вспомнил про журнал.
//
// argsOf отвечает на вопрос «о чем спросили»: у каждого инструмента он свой,
// потому что общего поля «запрос» у них нет, а выдуманное общее означало бы
// пустую строку у stats и ложную «пустой запрос» у поиска.
func addTool[In any](
	s *mcp.Server,
	j journal,
	t *mcp.Tool,
	argsOf func(In) []string,
	h func(In) (map[string]any, error),
) {
	addToolCtx(s, j, t, argsOf, func(_ context.Context, in In) (map[string]any, error) { return h(in) })
}

// addToolCtx is addTool for a tool that goes to the network: контекст вызова
// (отмена клиентом, остановка сервера) доходит до запросов.
func addToolCtx[In any](
	s *mcp.Server,
	j journal,
	t *mcp.Tool,
	argsOf func(In) []string,
	h func(context.Context, In) (map[string]any, error),
) {
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		startedAt := j.now()
		out, err := h(ctx, in)
		j.call(t.Name, argsOf(in), startedAt, err)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(out)
	})
}
