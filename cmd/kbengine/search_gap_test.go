package main

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Индекс снят в один момент, каталог живёт дальше. Пока движок об этом молчит,
// «смысловой слой не нашёл» и «смысловой слой этой записи не видел» выглядят
// одинаково — и человек заключит, что темы в базе нет (#254).
func TestIndexGapLine(t *testing.T) {
	ix := search.Index{Built: "2026-08-19", Vectors: map[int]search.Vector{1: {1}, 2: {1}}}

	t.Run("называет и сколько, и с каких пор", func(t *testing.T) {
		got := indexGapLine(ix, []int{1, 2, 7, 9, 11})
		for _, want := range []string{"3", "5", "2026-08-19"} {
			if !strings.Contains(got, want) {
				t.Errorf("в строке %q нет %q", got, want)
			}
		}
	})

	// Отрицательный контроль приёмки: при совпадении чисел молчим. Строка,
	// приходящая всегда, перестаёт читаться через неделю.
	t.Run("полное покрытие — молчание", func(t *testing.T) {
		if got := indexGapLine(ix, []int{1, 2}); got != "" {
			t.Errorf("покрытый каталог не повод для строки: %q", got)
		}
	})

	// Индекса нет вовсе — это забота другого сообщения, и дублировать его
	// здесь значило бы сказать человеку одно и то же двумя способами.
	t.Run("пустой индекс молчит — про него скажет слой", func(t *testing.T) {
		if got := indexGapLine(search.Index{}, []int{1, 2}); got != "" {
			t.Errorf("отсутствие индекса не пробел индекса: %q", got)
		}
	})

	// Момент в файле записан полным RFC3339 со смещением зоны. Человеку в
	// строке предупреждения нужен день, а не секунды с часовым поясом: длинная
	// метка рвёт строку и читается хуже, чем не читается вовсе.
	t.Run("дата сокращается до дня", func(t *testing.T) {
		full := search.Index{Built: "2026-08-19T19:04:41+05:00", Vectors: map[int]search.Vector{1: {1}}}
		got := indexGapLine(full, []int{1, 5})
		if !strings.Contains(got, "2026-08-19") || strings.Contains(got, "19:04") {
			t.Errorf("строка выглядит так: %q", got)
		}
	})

	// Дата в файле необязательна: старые индексы её не несут. Тогда честно
	// говорим только про число, а не выдумываем «неизвестно когда».
	t.Run("без даты говорит только про число", func(t *testing.T) {
		got := indexGapLine(search.Index{Vectors: map[int]search.Vector{1: {1}}}, []int{1, 5})
		if got == "" || strings.Contains(got, "снят") {
			t.Errorf("строка без даты выглядит так: %q", got)
		}
	})
}
