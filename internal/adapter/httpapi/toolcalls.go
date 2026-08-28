package httpapi

import (
	"net/http"

	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// Заготовка: порт и опция объявлены, ответа ещё нет — тесты рядом красные.

// ToolCallReader — порт журнала вызовов.
type ToolCallReader interface {
	Calls(limit int) ([]runs.Call, error)
}

// WithToolCalls подключает журнал вызовов к витрине.
func WithToolCalls(r ToolCallReader) Option {
	return func(o *options) { o.calls = r }
}

func handleToolCalls(_ ToolCallReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]any{}) }
}
