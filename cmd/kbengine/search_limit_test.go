package main

import (
	"bytes"
	"strings"
	"testing"
)

// Поиск объявляет число по ПОЛНОМУ набору, а печатает `--limit` записей. Пока
// об обрезке не сказано, «текстовый слой — 13» над списком из десяти строк
// читается как сбой, а не как предел: на живом каталоге это стоило получаса
// доказательств, что две поверхности движка не разошлись.
//
// Правило то же, что у пробела индекса рядом: инструмент обязан называть, чего
// он НЕ показал, и молчать, когда показывать нечего.
func TestLimitLine(t *testing.T) {
	t.Run("список обрезан — предел назван", func(t *testing.T) {
		got := limitLine(10, 13)
		for _, want := range []string{"10", "13", "limit"} {
			if !strings.Contains(got, want) {
				t.Errorf("в строке %q нет %q", got, want)
			}
		}
	})

	// Отрицательный контроль: строка, приходящая всегда, перестаёт читаться.
	t.Run("влезло всё — молчим", func(t *testing.T) {
		if got := limitLine(13, 13); got != "" {
			t.Errorf("целый список не повод для строки: %q", got)
		}
	})

	t.Run("не нашлось ничего — молчим", func(t *testing.T) {
		if got := limitLine(0, 0); got != "" {
			t.Errorf("пустая выдача не повод для строки: %q", got)
		}
	})
}

// Сквозная половина: предел должен доехать до экрана, а не только до функции.
func TestSearchNamesTheLimitOnScreen(t *testing.T) {
	path := baseWithCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"search", "--catalog", path, "--q", "entry", "--limit", "1"}, &out, &errb); code != 0 {
		t.Fatalf("search вернул %d: %s", code, out.String()+errb.String())
	}
	said := out.String()
	if !strings.Contains(said, "limit") {
		t.Errorf("обрезка не названа на экране:\n%s", said)
	}
}
