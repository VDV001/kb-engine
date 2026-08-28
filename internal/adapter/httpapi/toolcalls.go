package httpapi

import (
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// Сколько вызовов отдаётся странице за раз.
//
// Двести — суждение, а не замер: журнал растёт вечно, а вкладка отвечает на
// вопрос «о чём спрашивали в последнее время». Флага нет намеренно: предел,
// который каждый передаёт свой, превращается в способ показать другое число, не
// признав, что вопрос был другой.
const toolCallLimit = 200

// ToolCallReader — порт журнала вызовов. Объявлен здесь, в пакете-потребителе:
// направление зависимости решает тот, кто спрашивает.
type ToolCallReader interface {
	Calls(limit int) ([]runs.Call, error)
}

// WithToolCalls подключает журнал вызовов к витрине.
//
// Опцией, а не десятым аргументом NewServer: журнала может не быть вовсе, и это
// законное состояние — движок поднимают и без него.
func WithToolCalls(r ToolCallReader) Option {
	return func(o *options) { o.calls = r }
}

// toolCallDTO — вызов так, как его видит страница.
//
// ⚠️ Полей ровно четыре, и `query` среди них — единственное значение аргумента,
// которое движок отдаёт наружу. Граница проходит по виду записи: usecase кладёт
// сюда только вызовы инструментов, аргументы команд (`fin add --amount …`) в
// этот список не попадают по конструкции.
type toolCallDTO struct {
	Tool  string    `json:"tool"`
	Query string    `json:"query,omitempty"`
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
}

// handleToolCalls отвечает тем, что агент спрашивал у базы.
//
// Без журнала — не отказ, а честное «его нет»: 404 на странице читается как
// поломка витрины, а пустой список — как «агент базу не спрашивал». Это разные
// ответы, и различить их снаружи можно только полем.
func handleToolCalls(r ToolCallReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if r == nil {
			writeJSON(w, map[string]any{"exists": false, "total": 0, "calls": []toolCallDTO{}})
			return
		}
		calls, err := r.Calls(toolCallLimit)
		if err != nil {
			// Нечитаемый журнал — настоящая ошибка, и молчать о ней нельзя:
			// пустая страница выглядела бы как «вызовов не было».
			http.Error(w, "журнал вызовов не читается: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]toolCallDTO, 0, len(calls))
		for _, c := range calls {
			out = append(out, toolCallDTO{Tool: c.Tool, Query: c.Query, At: c.At, OK: c.OK})
		}
		writeJSON(w, map[string]any{"exists": true, "total": len(out), "calls": out})
	}
}
