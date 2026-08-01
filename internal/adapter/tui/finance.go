package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// FinanceLoader is what the finances screen needs from the ledger: a report
// over a period. It lives here, in the consumer, and it is the same shape the
// HTTP API asks for — the totals are computed in one place for both surfaces,
// so the terminal and the dashboard cannot drift into different numbers.
//
// The period is an argument rather than something the screen narrows itself:
// handing the whole history over and re-totalling here would be a second
// implementation of the arithmetic that has to agree with the first.
type FinanceLoader interface {
	Summary(months []string) (finance.Summary, error)
}

// WithFinances attaches the ledger to the screen. Without it the finances key
// does nothing at all rather than opening an empty screen — the rule the
// editing keys already follow.
func (m Model) WithFinances(fin FinanceLoader) Model {
	m.finances = fin
	return m
}

// OnFinances reports whether the finances screen is open.
func (m Model) OnFinances() bool { return m.onFinances }

// openFinances loads the report and shows it. A failure is kept and named on
// the screen: a finances view that silently shows zeroes cannot be told apart
// from a month with no spending.
func (m Model) openFinances() (tea.Model, tea.Cmd) {
	m.onFinances = true
	m.summary, m.finErr = m.finances.Summary(nil)
	return m, nil
}

// updateFinances handles one key on the finances screen.
func (m Model) updateFinances(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyTab, tea.KeyEsc:
		m.onFinances = false
		return m, nil
	}
	return m, nil
}

func (m Model) renderFinances() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("финансы: за всё время") + "\n\n")

	if m.finErr != nil {
		b.WriteString(styleTitle.Render("отчёт не построен") + "\n")
		b.WriteString(m.finErr.Error() + "\n")
		return b.String() + "\n" + styleDim.Render(hintFinances)
	}

	s := m.summary
	fmt.Fprintf(&b, "расходы %s (%d)  ·  доходы %s (%d)  ·  итог %s\n\n",
		s.Expenses, s.ExpenseCount, s.Income, s.IncomeCount, s.Net)

	writeTotals(&b, "по категориям", s.ByCategory)
	writeTotals(&b, "по счетам", s.ByAccount)

	return b.String() + "\n" + styleDim.Render(hintFinances)
}

// writeTotals prints one breakdown. An empty breakdown says so rather than
// leaving a blank space that reads as "nothing was spent".
func writeTotals(b *strings.Builder, title string, rows []finance.CategoryTotal) {
	b.WriteString(styleTitle.Render(title) + "\n")
	if len(rows) == 0 {
		b.WriteString(styleDim.Render("  нет данных") + "\n\n")
		return
	}
	for _, r := range rows[:min(len(rows), financeRows)] {
		fmt.Fprintf(b, "  %-24s %12s  %3d\n", r.Category, r.Total, r.Count)
	}
	if len(rows) > financeRows {
		b.WriteString(styleDim.Render(fmt.Sprintf("  … ещё %d\n", len(rows)-financeRows)))
	}
	b.WriteString("\n")
}

const (
	// financeRows caps each breakdown so both fit one screen.
	financeRows  = 8
	hintFinances = "Tab — назад к поиску · Esc — назад · Ctrl+C — выход"
)
