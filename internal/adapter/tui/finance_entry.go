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
	// Add возвращает подстановки написания вместе с ошибкой: движок мог
	// записать «Транспорт» там, где человек набрал «транспорт», и умолчать
	// об этом значит показать на экране не то, что попало в файл.
	Add(finance.AddParams) ([]finance.Correction, error)
}

// WithFinanceWriter lets the finances screen record an entry. Without it the
// screen stays read-only and offers no entry keys at all — a key that opens a
// form nothing can save is worse than no key.
func (m Model) WithFinanceWriter(w FinanceWriter) Model {
	m.ledger = w
	return m
}

// WorkbookSyncer carries written rows over to the spreadsheet and returns what
// the sync did, in the words the command uses. The screen shows that line
// rather than inventing its own — the terminal and this screen cannot then
// describe the same run differently.
type WorkbookSyncer interface {
	Sync() (string, error)
}

// WithWorkbookSyncer lets the finances screen catch the workbook up. Without a
// workbook there is nothing to sync with, so the key is absent rather than
// present and inert.
func (m Model) WithWorkbookSyncer(s WorkbookSyncer) Model {
	m.syncer = s
	return m
}

// syncWorkbook runs the sync and reports it in one line.
//
// The note about the workbook lagging is dropped only on success: after a
// refusal the book really is still behind, and clearing the warning then would
// leave the person believing the opposite of what happened.
func (m Model) syncWorkbook() Model {
	if m.syncer == nil {
		return m
	}
	report, err := m.syncer.Sync()
	if err != nil {
		m.finStatus = fmt.Sprintf("не синхронизировано: %v", err)
		return m
	}
	m.finStatus = report
	m.workbookBehind = false
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
	// editing непусто, когда форма правит существующую запись: тогда Enter
	// не добавляет новую строку, а переписывает названную.
	editing string
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
	amount, err := parseAmount(f.value(fieldAmount))
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
	case tea.KeyEnter:
		// Форма одна на два намерения, и решает не клавиша, а то, открыта ли она
		// на существующей записи: добавить новую строку там, где человек правил
		// старую, — ошибка, которую он увидит только в отчёте.
		if m.form.editing != "" {
			return m.submitEdit(), nil
		}
		return m.submitForm(), nil
	default:
		m.form.fields, m.form.cursor = typeIntoFields(m.form.fields, m.form.cursor, msg)
	}
	return m, nil
}

// typeIntoFields applies one key to a list of labelled fields: moving between
// them, adding a rune, taking one back.
//
// Shared by both forms on the finances screen rather than written twice. Two
// copies of "what Tab does here" are how one form ends up stopping at the last
// field while the other wraps, and nobody notices until a value lands in the
// wrong place.
func typeIntoFields(fields []field, cursor int, msg tea.KeyMsg) ([]field, int) {
	switch msg.Type {
	case tea.KeyTab, tea.KeyDown:
		return fields, min(cursor+1, len(fields)-1)
	case tea.KeyShiftTab, tea.KeyUp:
		return fields, max(cursor-1, 0)
	case tea.KeyBackspace:
		if v := fields[cursor].value; v != "" {
			runes := []rune(v)
			fields[cursor].value = string(runes[:len(runes)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		fields[cursor].value += string(msg.Runes)
	}
	return fields, cursor
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
	fixed, err := m.ledger.Add(p)
	if err != nil {
		m.form.err = fmt.Sprintf("не записано: %v", err)
		return m
	}

	// Re-read rather than add the row to the totals in memory: after a write the
	// screen must show what the ledger says, not what the write hoped to do.
	m.form = entryForm{}
	m.finStatus = fmt.Sprintf("записано: %s %s %s", kindName(p.Kind), p.Amount, or(p.Category, p.Source)) +
		correctionNote(fixed)
	m.workbookBehind = true
	m.summary, m.finErr = m.finances.Summary(nil)
	return m
}

func (m Model) renderForm() string {
	var b strings.Builder
	title := "новая запись: " + kindName(m.form.kind)
	if m.form.editing != "" {
		title = "правка записи: " + kindName(m.form.kind)
	}
	b.WriteString(styleQuery.Render(title) + "\n\n")

	for i, f := range m.form.fields {
		value := f.value
		if value == "" && m.form.editing == "" {
			// Пустое поле показывает пример того, что в него кладут. Одно
			// название поля этого не объясняет: «счёт» прочли как что угодно,
			// кроме банка, и трата ушла в ledger ничьей.
			//
			// В правке примеров нет: там пустое поле означает, что у записи это
			// значение пустое, и подсказка читалась бы как её содержимое.
			value = styleDim.Render(m.hintFor(f.label))
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
	hint := hintForm
	if m.form.editing != "" {
		hint = hintEditForm
	}
	return b.String() + "\n" + styleDim.Render(hint)
}

// hintFor возвращает пример для пустого поля.
//
// Счёт стоит особняком: его подсказка — не выдуманное имя, а те счета, что
// действительно лежат на листе «Счета». Выдуманный пример был бы хуже пустоты,
// потому что набранный дословно он получил бы отказ при записи. Когда книга не
// подключена, поле говорит об этом флагом, а не молчит: молчание здесь читается
// как «счёт не нужен».
func (m Model) hintFor(label string) string {
	switch label {
	case fieldAmount:
		return "418 или 418,50"
	case fieldCategory:
		return "Транспорт"
	case fieldSubcategory:
		return "Такси"
	case fieldPlace:
		return "Яндекс Такси"
	case fieldNote:
		return "до центра"
	case fieldSource:
		return "Зарплата"
	case fieldDate:
		// Пустая дата — не пропущенное значение, а сегодня.
		return "сегодня"
	case fieldAccount:
		return m.accountHint()
	case fieldBalance:
		// Пример показывает заодно форму записи: копейки через запятую
		// принимаются наравне с точкой, и больше об этом сказать негде.
		return "4321,55"
	}
	return ""
}

// accountHint перечисляет счета книги — до трёх, чтобы строка не разъезжалась.
func (m Model) accountHint() string {
	if m.accounts == nil {
		return "книга не подключена (--from)"
	}
	list := m.accountSnapshot
	if m.accountErr != nil || len(list) == 0 {
		return "счетов в книге нет"
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Bank)
	}
	if len(names) > 3 {
		return strings.Join(names[:3], " · ") + " · …"
	}
	return strings.Join(names, " · ")
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
	fieldBalance     = "баланс"

	hintForm     = "Tab/↑↓ — поле · Enter — записать · Esc — отмена"
	hintEditForm = "Tab/↑↓ — поле · Enter — сохранить · Esc — назад к списку"
)

// correctionNote дописывает к статусу то, что движок поправил при записи.
//
// Без неё экран показывал бы набранное, а в файл уходило бы другое: движок
// приводит написание к тому, что в базе уже есть, и человек имеет право видеть
// разницу сразу, а не найти её в отчёте через месяц.
func correctionNote(fixed []finance.Correction) string {
	if len(fixed) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fixed))
	for _, c := range fixed {
		parts = append(parts, fmt.Sprintf("%s → %s", c.Typed, c.Used))
	}
	return " · поправлено: " + strings.Join(parts, ", ")
}
