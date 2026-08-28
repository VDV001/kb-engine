package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Recorder — порт журнала вызовов: кто и о чём спросил каталог.
//
// Метод не возвращает ошибку намеренно. Журнал — наблюдатель, а не участник:
// его поломка не имеет права изменить ответ агенту, а решать, что делать со
// сломанным журналом, обязана точка сборки — у неё есть stderr, которого у
// адаптера нет (и печать из internal/ запрещена гейтом arch #5).
type Recorder interface {
	RecordCall(tool string, args []string, startedAt time.Time, took time.Duration, exitCode int)
}

// Config — то, что сервер узнаёт снаружи.
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
}

// journal — счётчик вызовов, приделанный к регистрации инструментов.
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
// выглядит в счётчике как удачный вызов — та же форма 9, что у проверки,
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
// Журнал приделан к РЕГИСТРАЦИИ, а не к телу обработчика — тот же приём, что у
// runLogged вокруг единственного диспетчера команд: новый инструмент попадает в
// счётчик тем, что он объявлен, а не тем, что автор вспомнил про журнал.
//
// argsOf отвечает на вопрос «о чём спросили»: у каждого инструмента он свой,
// потому что общего поля «запрос» у них нет, а выдуманное общее означало бы
// пустую строку у stats и ложную «пустой запрос» у поиска.
func addTool[In any](
	s *mcp.Server,
	j journal,
	t *mcp.Tool,
	argsOf func(In) []string,
	h func(In) (map[string]any, error),
) {
	mcp.AddTool(s, t, func(_ context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		startedAt := j.now()
		out, err := h(in)
		j.call(t.Name, argsOf(in), startedAt, err)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(out)
	})
}
