package tui_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Суммы на экране читаются человеком, а не парсером: разряды разделены, как в
// дашборде. 1234567.89 и 1 234 567,89 — одно число, но найти в первом ошибку
// на порядок глазом нельзя.
func TestFinances_amountsAreReadable(t *testing.T) {
	fin := &stubFinances{sum: finance.Summary{
		ExpenseCount: 12, IncomeCount: 3,
		Expenses: amountOf(t, "1234567.89"), Income: amountOf(t, "7654321.00"), Net: amountOf(t, "6419753.11"),
	}}
	m := tui.NewModel(nil).WithFinances(fin)

	view := press(m, tab()).View()

	for _, want := range []string{"1 234 567,89", "7 654 321,00", "6 419 753,11"} {
		if !strings.Contains(view, want) {
			t.Errorf("на экране нет %q — суммы без разрядов\n--- view ---\n%s", want, view)
		}
	}
}

// «Расходы по счетам», а не «по счетам»: рядом стоят балансы, и одно и то же
// слово над разными величинами уже стоило владельцу получаса сверки с вебом.
func TestFinances_breakdownsSayTheyAreExpenses(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin).WithAccounts(accountsStub(t))

	view := press(m, tab()).View()

	for _, want := range []string{"расходы по категориям", "расходы по счетам", "на счетах"} {
		if !strings.Contains(view, want) {
			t.Errorf("на экране нет подписи %q\n--- view ---\n%s", want, view)
		}
	}
}

// Экран поиска называет Tab. Клавиша работала, а строка внизу молчала — второй
// экран существовал, и ничто на первом об этом не говорило.
func TestList_hintNamesTab(t *testing.T) {
	withFin := tui.NewModel(nil).WithFinances(&stubFinances{sum: sampleSummary()})
	if view := withFin.View(); !strings.Contains(view, "Tab — финансы") {
		t.Errorf("подсказка не называет Tab\n--- view ---\n%s", view)
	}

	// Без единого второго экрана Tab не упоминается: клавиша, которая ничего не
	// делает, в подсказке хуже её отсутствия.
	if view := tui.NewModel(nil).View(); strings.Contains(view, "Tab") {
		t.Errorf("подсказка обещает Tab без второго экрана\n--- view ---\n%s", view)
	}
}

func amountOf(t *testing.T, raw string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(raw)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", raw, err)
	}
	return m
}
