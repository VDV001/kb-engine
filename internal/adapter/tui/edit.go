package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
)

// Edit is one requested change to one entry. Empty fields are not asked for —
// the same rule the engine's set command follows, so a forgotten field never
// wipes anything.
type Edit struct {
	ID        int
	Lifecycle string
	Verdict   string
}

// EntrySaver writes an edit to the catalog. Declared here, in the consumer, so
// the screen states what it needs rather than importing a storage package.
type EntrySaver interface {
	Save(Edit) error
}

// picker is the open list of values to choose from. A zero picker means the
// screen is not asking for anything.
type picker struct {
	field   string
	options []string
	cursor  int
}

func (p picker) open() bool { return len(p.options) > 0 }

// NewEditableModel returns the screen with editing enabled. Without a saver the
// screen stays read-only: an editing key that does nothing is worse than no key
// at all, so the read-only model does not offer the choice.
func NewEditableModel(entries []domain.Entry, saver EntrySaver, loader EntryLoader) Model {
	m := NewModel(entries)
	m.saver = saver
	m.loader = loader
	return m
}

// updatePicker handles the value-choice list. It owns Esc and Enter so a
// cancelled edit leaves the card exactly as it was.
func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.picker = picker{}
		m.status = ""
	case tea.KeyUp:
		m.picker.cursor = max(m.picker.cursor-1, 0)
	case tea.KeyDown:
		m.picker.cursor = min(m.picker.cursor+1, len(m.picker.options)-1)
	case tea.KeyEnter:
		return m.applyPick(), nil
	}
	return m, nil
}

// applyPick writes the chosen value and re-reads the catalog. Re-reading rather
// than patching the entry in memory: after a write the screen must show what the
// file says, not what it hoped the write would do.
func (m Model) applyPick() Model {
	chosen := m.picker.options[m.picker.cursor]
	edit := Edit{ID: m.visible[m.cursor].ID()}
	switch m.picker.field {
	case fieldLifecycle:
		edit.Lifecycle = chosen
	case fieldVerdict:
		edit.Verdict = chosen
	}

	if err := m.saver.Save(edit); err != nil {
		// The refusal is the point: a swallowed error means the person walks
		// away believing the change landed while the file still says otherwise.
		m.status = fmt.Sprintf("не записано: %v", err)
		m.picker = picker{}
		return m
	}

	// Name the field before clearing the picker: afterwards it no longer knows
	// which field was being chosen.
	note := fmt.Sprintf("записано: %s = %s", m.picker.fieldName(), chosen)
	m.picker = picker{}
	return m.reload(note)
}

func (m Model) reload(note string) Model {
	entries, err := m.loader.Entries()
	if err != nil {
		m.status = fmt.Sprintf("записано, но каталог не перечитан: %v", err)
		return m
	}
	m.all = entries
	m.status = note
	return m.search(m.query)
}

// openPicker starts a choice for the field, if this screen may write at all.
func (m Model) openPicker(field string) Model {
	if m.saver == nil || len(m.visible) == 0 {
		return m
	}
	var options []string
	switch field {
	case fieldLifecycle:
		options = domain.Lifecycles()
	case fieldVerdict:
		options = domain.Verdicts()
	}
	m.picker = picker{field: field, options: options, cursor: currentIndex(options, m.visible[m.cursor], field)}
	m.status = ""
	return m
}

// currentIndex starts the choice on the value the entry already has, so a
// mis-typed Enter changes nothing.
func currentIndex(options []string, e domain.Entry, field string) int {
	var current string
	switch field {
	case fieldLifecycle:
		current = e.Lifecycle().String()
	case fieldVerdict:
		if v := e.Verdict(); v != nil {
			current = v.String()
		}
	}
	for i, o := range options {
		if o == current {
			return i
		}
	}
	return 0
}

func (p picker) fieldName() string {
	if p.field == fieldVerdict {
		return "вердикт"
	}
	return "состояние"
}

func (m Model) renderPicker() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(m.visible[m.cursor].Title()) + "\n\n")
	b.WriteString(styleQuery.Render(m.picker.fieldName()+":") + "\n\n")
	for i, o := range m.picker.options {
		if i == m.picker.cursor {
			b.WriteString(styleSelected.Render("▸ "+o) + "\n")
			continue
		}
		b.WriteString("  " + o + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintPicker)
}

const (
	fieldLifecycle = "lifecycle"
	fieldVerdict   = "verdict"
	hintPicker     = "↑↓ — выбор · Enter — записать · Esc — отмена"
)
