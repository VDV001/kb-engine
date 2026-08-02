package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// FinanceWriter appends one entry to the ledger. It takes the use case's own
// parameters rather than raw strings: the rules around them — what an omitted
// date means, which fields an income may not carry — live in the use case and
// the domain, and a second surface that re-decided any of them would write rows
// the first one cannot.
//
// Declared here, in the consumer, like every other interface this screen needs.
type FinanceWriter interface {
	Add(finance.AddParams) error
}

// WithFinanceWriter lets the finances screen record an entry. Without it the
// screen stays read-only and offers no entry keys at all — a key that opens a
// form nothing can save is worse than no key.
func (m Model) WithFinanceWriter(w FinanceWriter) Model {
	m.ledger = w
	return m
}

// OnForm reports whether the entry form is open.
func (m Model) OnForm() bool { return m.form.open() }

// field is one line of the form: what it is called and what has been typed.
type field struct {
	label string
	value string
}

// entryForm is the open form. A zero form means nothing is being entered; the
// kind doubles as that flag because a form always records one or the other.
type entryForm struct {
	kind   string
	fields []field
	cursor int
	err    string
}

func (f entryForm) open() bool { return f.kind != "" }

// newEntryForm returns the fields the kind may carry.
//
// An income gets four of them, not seven: the domain refuses a category, a
// subcategory, a place and an account on an income, because Доходы has no
// column for any of them. Offering fields whose values would be rejected on
// submit teaches the wrong thing about the ledger.
func newEntryForm(kind string) entryForm {
	labels := []string{fieldAmount, fieldCategory, fieldSubcategory, fieldPlace, fieldAccount, fieldNote, fieldDate}
	if kind == domain.KindIncome {
		labels = []string{fieldAmount, fieldSource, fieldNote, fieldDate}
	}
	fields := make([]field, 0, len(labels))
	for _, l := range labels {
		fields = append(fields, field{label: l})
	}
	return entryForm{kind: kind, fields: fields}
}

// value returns what has been typed into the named field, empty when this kind
// does not have it.
func (f entryForm) value(label string) string {
	for _, fl := range f.fields {
		if fl.label == label {
			return strings.TrimSpace(fl.value)
		}
	}
	return ""
}

// params turns the typed lines into the use case's parameters, or names the
// field that cannot be read. The amount and the date are the only two that need
// interpreting, and both are reported by the name on screen so the person knows
// which line to fix.
//
// An empty date stays the zero value on purpose: today is the use case's
// default, and copying that rule here is how two surfaces start disagreeing
// about what "no date" means.
func (f entryForm) params() (finance.AddParams, error) {
	amount, err := domain.ParseMoney(f.value(fieldAmount))
	if err != nil {
		return finance.AddParams{}, fmt.Errorf("%s: %w", fieldAmount, err)
	}

	var date time.Time
	if raw := f.value(fieldDate); raw != "" {
		if date, err = time.Parse(time.DateOnly, raw); err != nil {
			return finance.AddParams{}, fmt.Errorf("%s %q: ожидается ГГГГ-ММ-ДД", fieldDate, raw)
		}
	}

	return finance.AddParams{
		Kind:        f.kind,
		Date:        date,
		Amount:      amount,
		Category:    f.value(fieldCategory),
		Subcategory: f.value(fieldSubcategory),
		Place:       f.value(fieldPlace),
		Description: f.value(fieldNote),
		Source:      f.value(fieldSource),
		Account:     f.value(fieldAccount),
	}, nil
}

// openForm starts an entry, if this screen may write at all.
func (m Model) openForm(kind string) Model {
	if m.ledger == nil {
		return m
	}
	m.form = newEntryForm(kind)
	m.finStatus = ""
	return m
}

// updateForm handles one key while an entry is being typed.
func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		// Back to the finances screen, not out of it: Esc cancels the entry, and
		// leaving the screen as well would cost a keystroke nobody asked for.
		m.form = entryForm{}
	case tea.KeyTab, tea.KeyDown:
		m.form.cursor = min(m.form.cursor+1, len(m.form.fields)-1)
	case tea.KeyShiftTab, tea.KeyUp:
		m.form.cursor = max(m.form.cursor-1, 0)
	case tea.KeyEnter:
		return m.submitForm(), nil
	case tea.KeyBackspace:
		if v := m.form.fields[m.form.cursor].value; v != "" {
			runes := []rune(v)
			m.form.fields[m.form.cursor].value = string(runes[:len(runes)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.form.fields[m.form.cursor].value += string(msg.Runes)
	}
	return m, nil
}

// submitForm writes the entry and re-reads the report.
//
// A refusal — from the parsing here, from the domain, or from the file — leaves
// the form exactly as it was. Clearing it would mean the amount, the place and
// the note are gone by the time the person reads why nothing was written.
func (m Model) submitForm() Model {
	p, err := m.form.params()
	if err != nil {
		m.form.err = err.Error()
		return m
	}
	if err := m.ledger.Add(p); err != nil {
		m.form.err = fmt.Sprintf("не записано: %v", err)
		return m
	}

	// Re-read rather than add the row to the totals in memory: after a write the
	// screen must show what the ledger says, not what the write hoped to do.
	m.form = entryForm{}
	m.finStatus = fmt.Sprintf("записано: %s %s %s", kindName(p.Kind), p.Amount, or(p.Category, p.Source))
	m.summary, m.finErr = m.finances.Summary(nil)
	return m
}

func (m Model) renderForm() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("новая запись: "+kindName(m.form.kind)) + "\n\n")

	for i, f := range m.form.fields {
		value := f.value
		if value == "" && f.label == fieldDate {
			// The default is worth showing: an empty date is not a missing value,
			// it is today.
			value = styleDim.Render("сегодня")
		}
		line := fmt.Sprintf("%-14s %s", f.label, value)
		if i == m.form.cursor {
			b.WriteString(styleSelected.Render("▸ "+line) + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}

	if m.form.err != "" {
		b.WriteString("\n" + styleTitle.Render(m.form.err) + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintForm)
}

func kindName(kind string) string {
	if kind == domain.KindIncome {
		return "доход"
	}
	return "расход"
}

const (
	fieldAmount      = "сумма"
	fieldCategory    = "категория"
	fieldSubcategory = "подкатегория"
	fieldPlace       = "место"
	fieldAccount     = "счёт"
	fieldNote        = "заметка"
	fieldDate        = "дата"
	fieldSource      = "источник"

	hintForm = "Tab/↑↓ — поле · Enter — записать · Esc — отмена"
)
