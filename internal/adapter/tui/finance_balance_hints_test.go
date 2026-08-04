package tui_test

import (
	"strings"
	"testing"
)

// Форма баланса выпала из правила, которое соблюдают все остальные формы:
// пустое поле показывает пример того, что в него кладут. Одно название поля
// этого не объясняет — владелец нажал Enter на пустой сумме и получил
// «invalid money: empty amount», то есть машинный текст вместо подсказки.
func TestBalanceForm_showsAnExampleInEmptyFields(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))

	view := press(press(m, tab()), runes("b")).View()

	// Сумма: пример показывает и форму записи — копейки через запятую.
	if !strings.Contains(view, "4321,55") {
		t.Errorf("поле баланса не показало пример\n--- view ---\n%s", view)
	}
	// Счёт: пример — не выдуманное имя, а счета, которые действительно лежат
	// на листе. Выдуманное, набранное дословно, получило бы отказ при записи.
	if !strings.Contains(view, "Сбербанк") {
		t.Errorf("поле счёта не показало счета книги\n--- view ---\n%s", view)
	}
}

// Пустое поле — это «не заполнено», а не «неверные деньги»: разные вещи, и
// человеку нужно знать, какая из двух с ним случилась. Английский текст
// доменной ошибки в русском экране не говорит ни о той, ни о другой.
func TestBalanceForm_namesAnEmptyAmountInRussian(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Сбербанк") // счёт заполнен, сумма — нет
	view := press(m, enter()).View()

	if strings.Contains(view, "invalid money") || strings.Contains(view, "empty amount") {
		t.Errorf("машинный текст ошибки показан человеку\n--- view ---\n%s", view)
	}
	if !strings.Contains(view, "сумма не введена") {
		t.Errorf("экран не сказал, чего не хватает\n--- view ---\n%s", view)
	}
}

// Набранное значение вытесняет пример: иначе поле показывает и то и другое, и
// непонятно, что именно уйдёт в книгу.
func TestBalanceForm_exampleDisappearsOnTyping(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Сбербанк")
	view := press(m, runes("500")).View()

	if strings.Contains(view, "4321,55") {
		t.Errorf("пример остался при набранном значении\n--- view ---\n%s", view)
	}
}
