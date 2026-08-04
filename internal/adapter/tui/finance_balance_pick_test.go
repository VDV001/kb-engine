package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func left() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyLeft} }
func right() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRight} }

// Форма просила набрать руками имя, которое движок только что показал списком.
// Отсюда и вопрос «зачем эта страница, если на экране всё видно»: она отбирала
// экран и требовала работы, которой не должно быть.
func TestBalanceForm_opensOnAnAccountFromTheBook(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))

	view := press(press(m, tab()), runes("b")).View()

	if !strings.Contains(view, "Сбербанк") {
		t.Errorf("форма открылась без выбранного счёта\n--- view ---\n%s", view)
	}
	// И сразу видно, что у этого счёта записано: подтверждая баланс, человек
	// решает, глядя на прежнее число, а форма закрывает собой экран, где оно
	// было.
	for _, want := range []string{"1 000,50", "28.07"} {
		if !strings.Contains(view, want) {
			t.Errorf("форма не показала, что записано сейчас (нет %q)\n--- view ---\n%s", want, view)
		}
	}
}

// Стрелки листают счета книги: набирать известное движку — работа, которой не
// должно быть, и каждая буква в ней может стать опечаткой.
func TestBalanceForm_arrowsWalkTheAccounts(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))
	m = press(press(m, tab()), runes("b"))

	view := press(m, right()).View()
	if !strings.Contains(view, "Альфа-Банк") || !strings.Contains(view, "1 507,12") {
		t.Errorf("→ не перевела на второй счёт\n--- view ---\n%s", view)
	}
	// По кругу: за последним снова первый. Тупик в конце списка человек читает
	// как поломку клавиши.
	if view := press(press(m, right()), right()).View(); !strings.Contains(view, "Сбербанк") {
		t.Errorf("список не замкнулся по кругу\n--- view ---\n%s", view)
	}
	if view := press(m, left()).View(); !strings.Contains(view, "Альфа-Банк") {
		t.Errorf("← не пошла в обратную сторону\n--- view ---\n%s", view)
	}
}

// Выбор не запирает форму: новый счёт всё ещё вводится буквами, иначе ctrl+n
// нечем воспользоваться — заводить было бы нечего.
func TestBalanceForm_typingReplacesTheChoice(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))
	m = press(press(m, tab()), runes("b"))

	m = press(m, runes("Долг → Отец"))
	view := m.View()

	if !strings.Contains(view, "Долг → Отец") {
		t.Errorf("набранное имя не встало в поле\n--- view ---\n%s", view)
	}
	// И форма говорит, что такого счёта на листе нет, — до нажатия Enter.
	if !strings.Contains(strings.ToLower(view), "ctrl+n") {
		t.Errorf("форма не сказала, что счёт незнакомый\n--- view ---\n%s", view)
	}
}

// Стрелки листают счёт только когда курсор на нём: на поле суммы они не должны
// незаметно менять счёт, в который уйдёт набранное число.
func TestBalanceForm_arrowsDoNothingOnTheAmountField(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))
	m = press(press(m, tab()), runes("b"))

	m = press(m, tab()) // курсор на сумму
	view := press(m, right()).View()

	if strings.Contains(view, "Альфа-Банк") {
		t.Errorf("счёт сменился со стрелки на поле суммы\n--- view ---\n%s", view)
	}
}
