package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
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
	case tea.KeyTab:
		return m.nextScreen()
	case tea.KeyEsc:
		// Esc — назад в поиск, а не следующий экран: выйти и перебирать дальше
		// это разные намерения, и один Tab для обоих отнимал бы у человека
		// возможность просто уйти.
		m.onFinances = false
		return m, nil
	case tea.KeyRunes:
		// Two letters start an entry, and only when this screen may write. The
		// kind is chosen by the key rather than inside the form: an expense and an
		// income do not carry the same fields, so the form has to know which one
		// it is before it can show anything.
		switch string(msg.Runes) {
		case "a":
			return m.openForm(domain.KindExpense), nil
		case "i":
			return m.openForm(domain.KindIncome), nil
		case "s":
			return m.syncWorkbook(), nil
		case "q":
			return m.openQuick(), nil
		case "b":
			return m.openBalance(), nil
		}
	}
	return m, nil
}

func (m Model) renderFinances() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("финансы: за всё время") + "\n\n")

	if m.finErr != nil {
		b.WriteString(styleTitle.Render("отчёт не построен") + "\n")
		b.WriteString(m.finErr.Error() + "\n")
		return b.String() + "\n" + m.financeHint()
	}

	s := m.summary
	fmt.Fprintf(&b, "расходы %s (%d)  ·  доходы %s (%d)  ·  итог %s\n\n",
		s.Expenses, s.ExpenseCount, s.Income, s.IncomeCount, s.Net)

	writeTotals(&b, "по категориям", s.ByCategory)
	writeTotals(&b, "по счетам", s.ByAccount)
	m.writeBalances(&b)

	if m.finStatus != "" {
		b.WriteString(styleQuery.Render(m.finStatus) + "\n")
	}
	if m.workbookBehind {
		// The workbook is a second file, and until it is caught up the engine
		// says so: the difference between knowing the book is behind and finding
		// out weeks later. With a workbook configured the key is named here, so
		// nobody has to leave the screen for it.
		note := hintWorkbook
		if m.syncer != nil {
			note = hintWorkbookKey
		}
		b.WriteString(styleDim.Render(note) + "\n")
	}

	return b.String() + "\n" + m.financeHint()
}

// financeHint lists only the keys this screen actually has. The entry keys are
// absent without a writer rather than present and inert.
func (m Model) financeHint() string {
	if m.ledger == nil {
		return styleDim.Render(hintFinances)
	}
	keys := hintFinancesWrite
	if m.vocab != nil {
		keys += " · " + hintQuickKey
	}
	if m.accounts != nil {
		keys += " · " + hintBalanceKey
	}
	if m.syncer != nil {
		keys += " · " + hintFinancesSync
	}
	return styleDim.Render(keys + " · " + hintFinances)
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
	financeRows       = 8
	hintFinances      = "Tab — назад к поиску · Esc — назад · Ctrl+C — выход"
	hintFinancesWrite = "a — расход · i — доход"
	hintFinancesSync  = "s — книга"
	hintBalanceKey    = "b — баланс"
	hintWorkbook      = "книга Учёт_финансов.xlsx не тронута — kbengine fin sync"
	hintWorkbookKey   = "книга Учёт_финансов.xlsx отстала — s синхронизирует"
)
