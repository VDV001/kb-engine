package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
)

// AccountsSource is what the finances screen needs about balances: what the
// banks say now, and the one way to change it. Declared here, in the consumer,
// like every other interface this screen uses.
//
// Reading and writing sit in one interface because they are one question — a
// balance is only worth changing on a screen that shows what it currently is.
type AccountsSource interface {
	Accounts() ([]domain.Account, error)
	SetBalance(bank string, amount domain.Money) error
}

// WithAccounts attaches the balances. Without them the key is absent and the
// block is not drawn at all, rather than showing an empty list that reads as
// "no accounts".
func (m Model) WithAccounts(a AccountsSource) Model {
	m.accounts = a
	return m
}

// balanceForm is the two-field entry: which account, and what it now holds.
type balanceForm struct {
	open   bool
	fields []field
	cursor int
	err    string
}

// OnBalanceForm reports whether the balance entry is open.
func (m Model) OnBalanceForm() bool { return m.balance.open }

func (m Model) openBalance() Model {
	if m.accounts == nil {
		return m
	}
	m = m.refreshAccounts()
	m.balance = balanceForm{
		open:   true,
		fields: []field{{label: fieldAccount}, {label: fieldBalance}},
	}
	m.finStatus = ""
	return m
}

func (m Model) updateBalance(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.balance = balanceForm{}
	case tea.KeyEnter:
		return m.submitBalance(), nil
	default:
		m.balance.fields, m.balance.cursor = typeIntoFields(m.balance.fields, m.balance.cursor, msg)
	}
	return m, nil
}

// submitBalance records the balance, or names why it did not.
//
// A refusal leaves the form as it was, the rule the entry form already follows:
// the bank the book does not know is a correctable mistake, and clearing the
// fields would take away what has to be corrected.
func (m Model) submitBalance() Model {
	bank := strings.TrimSpace(m.balance.fields[0].value)
	raw := strings.TrimSpace(m.balance.fields[1].value)

	// ParseMoney, not MoneyFromFloat: this is text a person typed, so more
	// precision than a kopeck is a typo and gets reported rather than rounded.
	amount, err := domain.ParseMoney(raw)
	if err != nil {
		m.balance.err = fmt.Sprintf("сумма: %v", err)
		return m
	}
	if err := m.accounts.SetBalance(bank, amount); err != nil {
		m.balance.err = fmt.Sprintf("не записано: %v", err)
		return m
	}

	m.balance = balanceForm{}
	m.finStatus = fmt.Sprintf("баланс: %s %s", bank, amount)
	// Снимок устарел ровно сейчас: баланс, который только что записали, должен
	// быть виден на экране, а не через один вход-выход.
	return m.refreshAccounts()
}

func (m Model) renderBalanceForm() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("баланс счёта") + "\n\n")

	for i, f := range m.balance.fields {
		line := fmt.Sprintf("%-14s %s", f.label, f.value)
		if i == m.balance.cursor {
			b.WriteString(styleSelected.Render("▸ "+line) + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}

	// The banks the book knows are listed under the form. The refusal names them
	// too, but reading them before typing is cheaper than reading them after.
	if names := m.accountNames(); names != "" {
		b.WriteString("\n" + styleDim.Render("на листе «Счета»: "+names) + "\n")
	}
	if m.balance.err != "" {
		b.WriteString("\n" + styleTitle.Render(m.balance.err) + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintForm)
}

func (m Model) accountNames() string {
	accs := m.accountSnapshot
	names := make([]string, 0, len(accs))
	for _, a := range accs {
		names = append(names, a.Bank())
	}
	return strings.Join(names, ", ")
}

// writeBalances prints what each account holds, with the date the number was
// last confirmed.
//
// A balance nobody has confirmed for months is not the same fact as one
// confirmed today, so the date is shown beside it rather than left in the file.
func (m Model) writeBalances(b *strings.Builder) {
	if m.accounts == nil {
		return
	}
	if m.accountErr != nil {
		b.WriteString(styleTitle.Render("балансы не прочитаны") + "\n")
		b.WriteString(m.accountErr.Error() + "\n\n")
		return
	}
	accs := m.accountSnapshot
	if len(accs) == 0 {
		return
	}

	// Итог сверху, счета под ним: первым читают «сколько у меня всего», а не
	// «сколько на Сбербанке». Это же число показывает веб как «на счетах».
	var total domain.Money
	for _, a := range accs {
		total = total.Add(a.Balance())
	}
	fmt.Fprintf(b, "%s %s\n", styleDim.Render("на счетах"), styleAccent.Render(human(total)))
	for _, a := range accs {
		when := "—"
		if !a.Updated().IsZero() {
			when = a.Updated().Format("02.01")
		}
		fmt.Fprintf(b, "  %-22s %12s  %s\n",
			trim(a.Bank(), 22), human(a.Balance()), styleDim.Render(when))
	}
	b.WriteString("\n")
}

// refreshAccounts перечитывает счета и запоминает результат.
//
// Единственное место, где книга читается ради экрана. Зовётся на входе и после
// записи — то есть тогда, когда данные могли измениться, а не на каждый кадр.
func (m Model) refreshAccounts() Model {
	if m.accounts == nil {
		m.accountSnapshot, m.accountErr = nil, nil
		return m
	}
	m.accountSnapshot, m.accountErr = m.accounts.Accounts()
	return m
}
