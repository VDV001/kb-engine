package tui_test

import (
	"strings"
	"testing"
)

// Незнакомое слово блокирует запись — и это правильно, гадать за человека
// движок не должен. Но чаще всего такое слово оказывается заметкой, и тогда
// единственным выходом было переписать строку целиком.
//
// Tab на этом экране свободен (буквы уходят в строку, свободных букв нет) и
// означает «эти слова — заметка».

func TestQuick_tabMovesUnknownWordsIntoTheNote(t *testing.T) {
	m, w := quickModelWithAccounts(t)

	m = press(press(m, tab()), runes("q"))
	m = press(m, runes("418р такси страховка"))
	m = press(m, enter()) // разобрать: «страховка» не опознана

	view := m.View()
	if !strings.Contains(view, "не знаю") {
		t.Fatalf("незнакомое слово не названо:\n%s", view)
	}
	if !strings.Contains(view, "Tab") {
		t.Errorf("экран не предлагает Tab для заметки:\n%s", view)
	}

	m = press(m, tab()) // забрать незнакомое в заметку

	// Метку и значение искать в ОДНОЙ строке разбора: само слово стоит ещё и в
	// набранной строке сверху, и проверка по нему проходила бы, ничего не
	// проверяя. Этот тест уже обманул один раз именно так.
	after := m.View()
	if strings.Contains(after, "не знаю") {
		t.Errorf("после Tab слово всё ещё числится незнакомым:\n%s", after)
	}
	shown := false
	for l := range strings.SplitSeq(after, "\n") {
		if strings.Contains(l, "заметка") && strings.Contains(l, "страховка") {
			shown = true
		}
	}
	if !shown {
		t.Errorf("после Tab заметки нет в разборе — не видно, куда ушло слово:\n%s", after)
	}

	press(m, enter()) // и записать

	if len(w.got) != 1 {
		t.Fatalf("записей %d, ожидалась 1 — незнакомое слово всё ещё блокирует", len(w.got))
	}
	if w.got[0].Description != "страховка" {
		t.Errorf("заметка = %q, ожидалась «страховка»", w.got[0].Description)
	}
}

// Без незнакомых слов Tab ничего не делает: подсказки нет, и перекладывать
// нечего.
func TestQuick_tabDoesNothingWithoutUnknownWords(t *testing.T) {
	m, _ := quickModelWithAccounts(t)

	m = press(press(m, tab()), runes("q"))
	m = press(m, runes("418р такси"))
	m = press(m, enter())
	view := press(m, tab()).View()

	if strings.Contains(view, "не знаю") {
		t.Errorf("взялись незнакомые слова из ниоткуда:\n%s", view)
	}
}
