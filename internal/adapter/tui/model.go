package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/theme"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// listWindow is how many rows the list shows at once. Fixed rather than read
// from the terminal because the height arrives in a WindowSizeMsg that the
// tests do not send; Update replaces it as soon as the real one arrives.
const listWindow = 15

// listChrome is how many lines the list screen spends on everything that is not
// an entry: the query, the counter and the blank after them (3), the blank and
// the "… ещё N" line under a long list (2), the blank and the hint at the
// bottom (2).
//
// It counts the tallest case, not the usual one. Counting six — the screen
// without "… ещё" — made the view one line taller than the window exactly when
// the list was long: the terminal scrolled, and the query line, the only thing
// saying what was typed, went off the top edge.
const listChrome = 7

// Model is the search screen: a query, the entries matching it, and either the
// list or one entry's card.
//
// Every field is unexported and every transition goes through Update, so the
// tests drive the screen exactly the way a person does — by pressing keys.
type Model struct {
	all     []domain.Entry
	visible []domain.Entry
	query   string
	cursor  int
	onCard  bool
	height  int

	// saver is nil on a read-only screen; picker is open while a value is being
	// chosen; status carries the last outcome to the person who caused it.
	saver  EntrySaver
	loader EntryLoader
	picker picker
	status string

	// finances is nil when no ledger is configured; summary and finErr hold the
	// last report and the reason it failed, so the screen can name either.
	finances   FinanceLoader
	onFinances bool
	summary    finance.Summary
	finErr     error

	// ledger is nil on a read-only finances screen; form is open while an entry
	// is being typed; finStatus carries the last write's outcome. Kept apart from
	// status above: that one belongs to the card, and a note about money written
	// here would otherwise reappear over an entry the person opened afterwards.
	// syncer is nil when no workbook is configured; workbookBehind says whether
	// a row written here has yet to reach the spreadsheet, so the screen asks for
	// a sync exactly when one is owed and stops asking once it is done.
	ledger         FinanceWriter
	syncer         WorkbookSyncer
	vocab          VocabularySource
	quick          quickForm
	form           entryForm
	finStatus      string
	workbookBehind bool
}

// NewModel returns the screen showing every entry.
func NewModel(entries []domain.Entry) Model {
	return Model{all: entries, visible: entries, height: listWindow}
}

// Init satisfies tea.Model; the screen has nothing to start.
func (m Model) Init() tea.Cmd { return nil }

// Visible returns the entries currently matching the query.
func (m Model) Visible() []domain.Entry { return m.visible }

// Cursor returns the index of the highlighted entry within Visible.
func (m Model) Cursor() int { return m.cursor }

// OnCard reports whether one entry is open.
func (m Model) OnCard() bool { return m.onCard }

// Update handles one key press.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = max(msg.Height-listChrome, 3)
		return m, nil
	case tea.KeyMsg:
		switch {
		case m.quick.open:
			return m.updateQuick(msg)
		case m.form.open():
			return m.updateForm(msg)
		case m.picker.open():
			return m.updatePicker(msg)
		case m.onFinances:
			return m.updateFinances(msg)
		case m.onCard:
			return m.updateCard(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m Model) updateCard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.onCard = false
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyRunes:
		// Letters on the card are not search — otherwise leaving the card would
		// silently change what the list behind it shows. Two of them ask for an
		// edit instead, and only when this screen may write.
		switch string(msg.Runes) {
		case "l":
			return m.openPicker(fieldLifecycle), nil
		case "v":
			return m.openPicker(fieldVerdict), nil
		}
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyUp:
		m.cursor = max(m.cursor-1, 0)
	case tea.KeyDown:
		m.cursor = min(m.cursor+1, max(len(m.visible)-1, 0))
	case tea.KeyEnter:
		if len(m.visible) > 0 {
			m.onCard = true
		}
	case tea.KeyTab:
		if m.finances != nil {
			return m.openFinances()
		}
	case tea.KeyBackspace:
		if m.query != "" {
			runes := []rune(m.query)
			m = m.search(string(runes[:len(runes)-1]))
		}
	case tea.KeyRunes, tea.KeySpace:
		m = m.search(m.query + string(msg.Runes))
	}
	return m, nil
}

// search re-runs the filter and pulls the cursor back inside the result: a
// cursor left beyond the end would point at an entry no longer on screen.
func (m Model) search(query string) Model {
	m.query = query
	m.visible = Filter(m.all, query)
	m.cursor = min(m.cursor, max(len(m.visible)-1, 0))
	return m
}

// View renders the screen.
func (m Model) View() string {
	switch {
	case m.quick.open:
		return m.renderQuick()
	case m.form.open():
		return m.renderForm()
	case m.picker.open():
		return m.renderPicker()
	case m.onFinances:
		return m.renderFinances()
	case m.onCard && len(m.visible) > 0:
		return m.renderCardWithStatus()
	default:
		return m.renderList()
	}
}

func (m Model) renderCardWithStatus() string {
	card := renderCard(m.visible[m.cursor])
	if m.saver != nil {
		card += "\n" + styleDim.Render(hintCardEdit)
	}
	if m.status != "" {
		card += "\n" + styleQuery.Render(m.status)
	}
	return card
}

func (m Model) renderList() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("поиск: "+m.query) + "\n")
	b.WriteString(styleDim.Render(fmt.Sprintf("%d из %d", len(m.visible), len(m.all))) + "\n\n")

	if len(m.visible) == 0 {
		b.WriteString(styleDim.Render("ничего не найдено") + "\n")
		return b.String() + "\n" + styleDim.Render(hintList)
	}

	for i, e := range m.window() {
		idx := m.first() + i
		line := fmt.Sprintf("%5d  %-9s %s", e.ID(), lifecycleOf(e), e.Title())
		if idx == m.cursor {
			b.WriteString(styleSelected.Render("▸ "+line) + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	if len(m.visible) > m.height {
		b.WriteString(styleDim.Render(fmt.Sprintf("\n… ещё %d", len(m.visible)-m.first()-len(m.window()))) + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintList)
}

// first is the index the visible window starts at — the cursor is kept in the
// middle of it once the list is longer than the screen.
func (m Model) first() int {
	if len(m.visible) <= m.height {
		return 0
	}
	return min(max(m.cursor-m.height/2, 0), len(m.visible)-m.height)
}

func (m Model) window() []domain.Entry {
	first := m.first()
	return m.visible[first:min(first+m.height, len(m.visible))]
}

func renderCard(e domain.Entry) string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(e.Title()) + "\n\n")

	rows := [][2]string{
		{"id", fmt.Sprint(e.ID())},
		{"категория", e.Category().String()},
		{"состояние", lifecycleOf(e)},
		{"вердикт", verdictOf(e)},
		{"автор", or(e.Author(), "—")},
		{"адрес", or(e.URL(), "—")},
		{"конспект", or(e.NotesFile(), "—")},
		{"теги", or(strings.Join(e.Tags(), ", "), "—")},
	}
	for _, r := range rows {
		b.WriteString(styleDim.Render(fmt.Sprintf("%-11s", r[0])) + r[1] + "\n")
	}
	if d := e.Description(); d != "" {
		b.WriteString("\n" + d + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintCard)
}

const (
	hintList     = "печатать — искать · ↑↓ — выбор · Enter — карточка · Esc — выход"
	hintCard     = "Esc — назад к списку"
	hintCardEdit = "l — состояние · v — вердикт"
)

func lifecycleOf(e domain.Entry) string { return e.Lifecycle().String() }

func verdictOf(e domain.Entry) string {
	if v := e.Verdict(); v != nil {
		return v.String()
	}
	if r := e.ReadState(); r != nil {
		return r.String()
	}
	return "—"
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Colours come from the same token file the web dashboard is generated from —
// the palette has one source, or it drifts apart within a month.
var (
	styleSelected = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.Primary })).Bold(true)
	styleTitle    = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.OnSurface })).Bold(true)
	styleQuery    = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.Secondary })).Bold(true)
	styleDim      = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.OnSurfaceVariant }))
)

func adaptive(pick func(theme.Palette) string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: pick(theme.Light), Dark: pick(theme.Dark)}
}
