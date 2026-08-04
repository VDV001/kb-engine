package tui_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Отрисовка не должна читать файлы.
//
// Владелец сообщил, что в форме баланса «буквы не появляются». Замер объяснил:
// чтение книги стоит 74 мс, а список счетов запрашивался из функции рендера —
// то есть на каждое нажатие клавиши. Восемь букв «Сбербанк» давали больше
// полусекунды отставания, и экран выглядел неживым.
//
// Тест считает обращения к источнику: после открытия формы их число не растёт,
// сколько бы кадров ни нарисовали.

type countingAccounts struct {
	inner *stubAccounts
	reads int
}

func (c *countingAccounts) Accounts() ([]domain.Account, error) {
	c.reads++
	return c.inner.Accounts()
}

// Balances — то самое обращение, стоимость которого измеряется: на живом файле
// оно читает книгу и леджер, и в отрисовке ему не место.
func (c *countingAccounts) Balances() ([]finance.AccountBalance, error) {
	c.reads++
	return c.inner.Balances()
}

func (c *countingAccounts) SetBalance(bank string, amount domain.Money) error {
	return c.inner.SetBalance(bank, amount)
}

func TestBalanceForm_doesNotReadTheBookWhileTyping(t *testing.T) {
	acc := &countingAccounts{inner: accountsStub(t)}
	fin := &stubFinances{sum: sampleSummary()}
	m := newModelWithAccounts(fin, acc)

	m = press(press(m, tab()), runes("b")) // открыть форму баланса
	after := acc.reads

	for _, r := range []string{"С", "б", "е", "р"} {
		m = press(m, runes(r))
		_ = m.View()
	}

	if acc.reads != after {
		t.Errorf("книга прочитана %d раз при наборе четырёх букв — чтение стоит 74 мс на живом файле",
			acc.reads-after)
	}
}

func TestEntryForm_doesNotReadTheBookWhileTyping(t *testing.T) {
	acc := &countingAccounts{inner: accountsStub(t)}
	fin := &stubFinances{sum: sampleSummary()}
	m := newModelWithAccounts(fin, acc)

	m = onForm(m, "a") // форма расхода: подсказка счёта тоже читает книгу
	after := acc.reads

	for _, r := range []string{"4", "1", "8"} {
		m = press(m, runes(r))
		_ = m.View()
	}

	if acc.reads != after {
		t.Errorf("книга прочитана %d раз при наборе трёх букв", acc.reads-after)
	}
}

func newModelWithAccounts(fin *stubFinances, acc *countingAccounts) tui.Model {
	return tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(&stubWriter{}).WithAccounts(acc)
}
