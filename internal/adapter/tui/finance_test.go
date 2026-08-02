package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// stubFinances stands in for the ledger. It records the period it was asked
// for, because taking the period as an argument is what keeps the arithmetic
// in one place for both surfaces.
type stubFinances struct {
	sum   finance.Summary
	err   error
	asked [][]string
}

func (s *stubFinances) Summary(months []string) (finance.Summary, error) {
	s.asked = append(s.asked, months)
	if s.err != nil {
		return finance.Summary{}, s.err
	}
	return s.sum, nil
}

func sampleSummary() finance.Summary {
	return finance.Summary{
		ExpenseCount: 3,
		Expenses:     domain.NewMoney(123400),
		IncomeCount:  1,
		Income:       domain.NewMoney(900000),
		Net:          domain.NewMoney(750000),
		ByCategory: []finance.CategoryTotal{
			{Category: "Еда", Total: domain.NewMoney(100000), Count: 2},
			{Category: "Транспорт", Total: domain.NewMoney(50000), Count: 1},
		},
		ByAccount: []finance.CategoryTotal{
			{Category: "Альфа-Банк", Total: domain.NewMoney(123400), Count: 3},
		},
	}
}

func tab() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyTab} }

func press(m tui.Model, msg tea.KeyMsg) tui.Model {
	next, _ := m.Update(msg)
	return next.(tui.Model)
}

func TestTabOpensFinancesWhenLedgerConfigured(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin)

	m = press(m, tab())

	if !m.OnFinances() {
		t.Fatal("Tab did not open the finances screen")
	}
	view := m.View()
	// Суммы — в том виде, в каком их читает человек: разряды разделены.
	for _, want := range []string{"Еда", "Транспорт", "Альфа-Банк", "1 234,00", "9 000,00"} {
		if !strings.Contains(view, want) {
			t.Errorf("finances view is missing %q\n--- view ---\n%s", want, view)
		}
	}
}

func TestTabReturnsFromFinancesToSearch(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin)

	m = press(press(m, tab()), tab())

	if m.OnFinances() {
		t.Fatal("second Tab did not return to the search screen")
	}
}

// Without a ledger the key does nothing rather than opening an empty screen:
// the same rule the editing keys already follow.
func TestTabDoesNothingWithoutLedger(t *testing.T) {
	m := tui.NewModel(nil)

	m = press(m, tab())

	if m.OnFinances() {
		t.Fatal("finances opened with no ledger configured")
	}
}

// A failure must be named on the screen. A finances view that silently shows
// zeroes is indistinguishable from a month with no spending.
func TestFinancesNamesTheFailure(t *testing.T) {
	fin := &stubFinances{err: errors.New("ledger is locked")}
	m := tui.NewModel(nil).WithFinances(fin)

	m = press(m, tab())

	view := m.View()
	if !strings.Contains(view, "ledger is locked") {
		t.Errorf("view does not name the failure\n--- view ---\n%s", view)
	}
}

// The whole history is the period the screen opens on, and it must be asked
// for explicitly — an empty argument is what "everything" means downstream.
func TestFinancesAsksForWholeHistory(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin)

	press(m, tab())

	if len(fin.asked) != 1 {
		t.Fatalf("ledger asked %d times, want 1", len(fin.asked))
	}
	if len(fin.asked[0]) != 0 {
		t.Errorf("opened on period %v, want the whole history", fin.asked[0])
	}
}
