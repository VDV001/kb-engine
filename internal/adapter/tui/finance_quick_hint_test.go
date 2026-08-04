package tui_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
)

// Пример строки жил в подсказке внизу экрана — «пример: 418р такси сбер» — и
// не объяснял, из чего строка состоит. Пустой экран после нажатия `q` не
// показывал ничего: человек видит приглашение и не знает, писать ли сумму
// первой, нужен ли банк и как его назвать.
//
// Пока строка пуста, экран показывает разбор примера по частям. С первым
// введённым символом разбор уступает место настоящему.

// quickModelWithAccounts собирает модель, у которой есть и словарь для разбора
// строки, и счета — пример должен называть настоящие банки, а не выдуманные.
func quickModelWithAccounts(t *testing.T) (tui.Model, *stubWriter) {
	t.Helper()
	fin := &stubFinances{sum: sampleSummary()}
	w := &stubWriter{}
	m := tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(w).
		WithVocabulary(knownWords()).WithAccounts(accountsStub(t))
	return m, w
}

func TestQuickEntryShowsExampleWhileEmpty(t *testing.T) {
	m, _ := quickModelWithAccounts(t)

	view := press(press(m, tab()), runes("q")).View()

	if !strings.Contains(view, "418") {
		t.Errorf("пустой быстрый ввод не показывает пример:\n%s", view)
	}
	// Разбор по частям: какое слово чем станет.
	for _, want := range []string{"сумма", "место", "счёт"} {
		if !strings.Contains(view, want) {
			t.Errorf("в разборе примера нет части %q:\n%s", want, view)
		}
	}
	// Счета — настоящие, как и в форме: выдуманный банк не разберётся.
	if !strings.Contains(view, "Сбербанк") {
		t.Errorf("пример не называет известные счета:\n%s", view)
	}
}

// Пример — не введённая строка. Если бы он ею становился, Enter записал бы
// чужую трату на 418 рублей.
func TestQuickExampleIsNotTheTypedLine(t *testing.T) {
	m, w := quickModelWithAccounts(t)

	m = press(press(m, tab()), runes("q"))
	m = press(m, enter()) // разобрать пустую строку
	press(m, enter())     // и попробовать записать

	if len(w.got) != 0 {
		t.Errorf("пример утёк в запись: записано %d строк, ожидалось 0", len(w.got))
	}
}

// С первым символом разбор примера уходит: иначе на экране стоят два разбора
// сразу — придуманный и настоящий.
func TestQuickExampleDisappearsOnFirstKey(t *testing.T) {
	m, _ := quickModelWithAccounts(t)

	m = press(press(m, tab()), runes("q"))
	view := press(m, runes("5")).View()

	if strings.Contains(view, "418") {
		t.Errorf("пример остался после начала ввода:\n%s", view)
	}
}
