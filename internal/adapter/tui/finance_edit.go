package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// EntryEditor — что экрану нужно для правки: какие записи показать и как
// изменить выбранную. Объявлен здесь, в потребителе, как и остальные интерфейсы
// этого экрана.
//
// Чтение и запись в одном интерфейсе, потому что это один вопрос: править
// имеет смысл на экране, который показывает, что именно правишь.
type EntryEditor interface {
	Recent(n int) ([]finance.Record, error)
	EditEntry(id string, p finance.EditParams) error
}

// WithEntryEditor включает правку. Без него клавиши нет вовсе: клавиша,
// открывающая экран, с которого нельзя сохранить, хуже отсутствующей.
func (m Model) WithEntryEditor(e EntryEditor) Model {
	m.editor = e
	return m
}

// recentCount — сколько записей показывать. Правят почти всегда последнее:
// ошибку замечают в тот же день, а не через сто записей.
const recentCount = 15

// entryList — список последних записей с курсором.
type entryList struct {
	open   bool
	recs   []finance.Record
	cursor int
	err    string
}

// OnEntryList reports whether the recent-entries list is open.
func (m Model) OnEntryList() bool { return m.entries.open }

func (m Model) openEntryList() Model {
	if m.editor == nil {
		return m
	}
	recs, err := m.editor.Recent(recentCount)
	m.entries = entryList{open: true, recs: recs}
	if err != nil {
		m.entries.err = fmt.Sprintf("записи не прочитаны: %v", err)
	}
	m.finStatus = ""
	return m
}

func (m Model) updateEntryList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.entries = entryList{}
	case tea.KeyUp:
		if m.entries.cursor > 0 {
			m.entries.cursor--
		}
	case tea.KeyDown:
		if m.entries.cursor < len(m.entries.recs)-1 {
			m.entries.cursor++
		}
	case tea.KeyEnter:
		return m.openEditForm(), nil
	}
	return m, nil
}

// openEditForm ставит форму на выбранную запись, заполнив её текущими
// значениями: правка начинается с того, что есть, а не с пустого бланка.
func (m Model) openEditForm() Model {
	if len(m.entries.recs) == 0 {
		return m
	}
	rec := m.entries.recs[m.entries.cursor]
	tx := rec.Transaction()

	m.form = newEntryForm(tx.Kind())
	m.form.editing = tx.ID()
	for i, f := range m.form.fields {
		switch f.label {
		case fieldAmount:
			m.form.fields[i].value = tx.Amount().String()
		case fieldCategory:
			m.form.fields[i].value = tx.Category()
		case fieldSubcategory:
			m.form.fields[i].value = tx.Subcategory()
		case fieldPlace:
			m.form.fields[i].value = tx.Place()
		case fieldAccount:
			m.form.fields[i].value = tx.Account()
		case fieldSource:
			m.form.fields[i].value = tx.Source()
		case fieldNote:
			m.form.fields[i].value = tx.Description()
		}
	}
	return m
}

// submitEdit отправляет правку тем же usecase, которым правит команда.
func (m Model) submitEdit() Model {
	p, err := m.form.editParams()
	if err != nil {
		m.form.err = err.Error()
		return m
	}
	if err := m.editor.EditEntry(m.form.editing, p); err != nil {
		m.form.err = fmt.Sprintf("не записано: %v", err)
		return m
	}

	id := m.form.editing
	m.form = entryForm{}
	m.finStatus = "исправлено: " + id
	m.workbookBehind = true
	m.summary, m.finErr = m.finances.Summary(nil)
	// Список перечитывается, а не правится в памяти: экран должен показывать
	// то, что в леджере, а не то, на что рассчитывала запись.
	if recs, err := m.editor.Recent(recentCount); err == nil {
		m.entries.recs = recs
	}
	return m
}

// editParams превращает поля формы в параметры правки. Пустое поле означает
// стирание: в форме правки, в отличие от командной строки, человек видит
// прежнее значение и стирает его руками — это и есть явное намерение.
func (f entryForm) editParams() (finance.EditParams, error) {
	p := finance.EditParams{
		Category:         f.value(fieldCategory),
		Subcategory:      f.value(fieldSubcategory),
		Place:            f.value(fieldPlace),
		Description:      f.value(fieldNote),
		Source:           f.value(fieldSource),
		Account:          f.value(fieldAccount),
		ClearSubcategory: f.value(fieldSubcategory) == "",
		ClearPlace:       f.value(fieldPlace) == "",
		ClearDescription: f.value(fieldNote) == "",
		ClearAccount:     f.value(fieldAccount) == "",
	}
	if raw := f.value(fieldAmount); raw != "" {
		amount, err := parseAmount(raw)
		if err != nil {
			return finance.EditParams{}, fmt.Errorf("%s: %w", fieldAmount, err)
		}
		p.Amount = amount
	}
	return p, nil
}

func (m Model) renderEntryList() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("последние записи") + "\n\n")

	if m.entries.err != "" {
		b.WriteString(styleTitle.Render(m.entries.err) + "\n")
	}
	if len(m.entries.recs) == 0 {
		b.WriteString(styleDim.Render("записей нет") + "\n")
		return b.String() + "\n" + styleDim.Render(hintEntryList)
	}

	for i, rec := range m.entries.recs {
		tx := rec.Transaction()
		// Счёт печатается всегда, и его отсутствие названо вслух: ради этой
		// строки экран и заводился — трата без счёта не попадает в разбивку,
		// а по остальным полям выглядит полной.
		account := tx.Account()
		if account == "" && tx.IsExpense() {
			account = "без счёта"
		}
		line := fmt.Sprintf("%-10s %10s  %-12s %-24s %s",
			tx.Date().Format("2006-01-02"), human(tx.Amount()),
			trim(tx.Category(), 12), trim(orDash(tx.Place(), tx.Description()), 24), account)
		if i == m.entries.cursor {
			b.WriteString(styleSelected.Render("▸ "+line) + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintEntryList)
}

func orDash(first, second string) string {
	if first != "" {
		return first
	}
	if second != "" {
		return second
	}
	return "—"
}

const (
	hintEntryList = "↑↓ — запись · Enter — править · Esc — назад"
	hintEditKey   = "e — править"
)
