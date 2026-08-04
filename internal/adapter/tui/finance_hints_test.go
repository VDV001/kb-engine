package tui_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
)

// Форма показывала семь пустых строк с одними названиями полей. Владелец
// заполнил расход, пропустив «счёт», — по названию не было видно, что это банк
// и что вписывать туда нужно «Сбербанк». Трата записалась ничьей: в разбивке по
// счетам её нет.
//
// Отсюда подсказки: пустое поле называет пример того, что в него кладут, а поле
// счёта — не выдуманный пример, а имена счетов, которые в книге действительно
// есть. Выдуманный пример здесь был бы хуже пустоты: набрав его дословно,
// человек получил бы отказ синка.

func hintedForm(t *testing.T) tui.Model {
	t.Helper()
	acc := accountsStub(t)
	m, _ := balanceModel(acc)
	return onForm(m, "a")
}

func TestFormNamesAnExampleForEveryEmptyField(t *testing.T) {
	view := hintedForm(t).View()

	for _, want := range []string{"418", "Транспорт", "Такси", "Яндекс Такси"} {
		if !strings.Contains(view, want) {
			t.Errorf("в форме нет примера %q:\n%s", want, view)
		}
	}
}

func TestAccountHintListsRealAccounts(t *testing.T) {
	view := hintedForm(t).View()

	// Имена приходят из книги, а не из кода: пример, которого нет на листе
	// «Счета», научил бы писать то, что синк потом отвергнет.
	for _, want := range []string{"Сбербанк", "Альфа-Банк"} {
		if !strings.Contains(view, want) {
			t.Errorf("подсказка счёта не называет %q:\n%s", want, view)
		}
	}
}

// Без --from список счетов взять неоткуда. Придумать его нельзя, промолчать
// тоже: пустая строка выглядит как «счёт не нужен». Форма says what it does not
// know — то же правило, по которому движок называет неподключённые источники.
func TestAccountHintSaysWhenAccountsAreNotConnected(t *testing.T) {
	m, _, _ := writable() // модель без WithAccounts
	view := onForm(m, "a").View()

	if !strings.Contains(view, "--from") {
		t.Errorf("без книги форма не говорит, почему не знает счетов:\n%s", view)
	}
}

// Подсказка — не значение. Если бы она попадала в запись, каждая трата без
// счёта уходила бы в ledger на первый попавшийся банк, и это было бы хуже
// сегодняшнего пропуска: неверные данные вместо отсутствующих.
func TestHintIsNotWrittenAsValue(t *testing.T) {
	acc := accountsStub(t)
	m, w := balanceModel(acc)

	m = onForm(m, "a")
	m = fill(m, "322")       // сумма
	m = fill(m, "Транспорт") // категория
	m = fill(m, "Такси")     // подкатегория
	m = fill(m, "")          // место — оставлено пустым
	m = fill(m, "")          // счёт — оставлен пустым
	m = fill(m, "до центра")
	press(m, enter())

	if len(w.got) != 1 {
		t.Fatalf("ожидалась одна запись, получено %d", len(w.got))
	}
	if got := w.got[0].Account; got != "" {
		t.Errorf("подсказка утекла в запись: Account = %q, ожидалось пустое", got)
	}
	if got := w.got[0].Place; got != "" {
		t.Errorf("подсказка утекла в запись: Place = %q, ожидалось пустое", got)
	}
}

// Введённое значение вытесняет подсказку — иначе на экране стояли бы оба.
func TestTypedValueReplacesHint(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = onForm(m, "a")
	m = fill(m, "418")
	view := m.View()

	if strings.Contains(view, "сумма          418") == false {
		t.Errorf("введённая сумма не показана:\n%s", view)
	}
}
