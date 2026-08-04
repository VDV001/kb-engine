package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/theme"
	"github.com/daniil/kb-engine/internal/usecase/audit"
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
	ledger   FinanceWriter
	syncer   WorkbookSyncer
	vocab    VocabularySource
	accounts AccountsSource
	quick    quickForm
	balance  balanceForm
	form     entryForm
	// accountSnapshot — счета, прочитанные при входе на экран финансов.
	//
	// Снимок, а не запрос по месту: чтение книги стоит 74 мс на живом файле, а
	// список нужен отрисовке — то есть каждому нажатию клавиши. Владелец увидел
	// это как «буквы не появляются»: восемь букв давали больше полусекунды
	// отставания. Отрисовка обязана быть дешёвой и не ходить в файлы.
	accountSnapshot []domain.Account
	accountErr      error

	// editor is nil when nothing can rewrite an entry, and then the key is
	// absent rather than opening a screen that cannot save.
	editor         EntryEditor
	entries        entryList
	finStatus      string
	workbookBehind bool

	// health is nil when no audit source is configured, and then the screen is
	// absent from the Tab cycle rather than present and empty. The summary and
	// the reason it failed are kept apart, so the screen can name either.
	health        HealthSource
	onHealth      bool
	healthSummary audit.Health
	healthErr     error
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
		case m.balance.open:
			return m.updateBalance(msg)
		case m.form.open():
			return m.updateForm(msg)
		case m.entries.open:
			return m.updateEntryList(msg)
		case m.picker.open():
			return m.updatePicker(msg)
		case m.onFinances:
			return m.updateFinances(msg)
		case m.onHealth:
			return m.updateHealth(msg)
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
		return m.nextScreen()
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

// nextScreen moves to the next screen in the cycle: search → finances → health
// → search.
//
// A cycle rather than a key per screen, because on the search screen every rune
// goes into the query — there are no free letters there to hang a second screen
// on. A screen with no source configured is not in the cycle at all, the rule
// the writing keys already follow: absent beats present and inert.
func (m Model) nextScreen() (tea.Model, tea.Cmd) {
	switch {
	case m.onFinances:
		m.onFinances = false
		if m.health != nil {
			return m.openHealth()
		}
		return m, nil
	case m.onHealth:
		m.onHealth = false
		return m, nil
	default:
		if m.finances != nil {
			return m.openFinances()
		}
		if m.health != nil {
			return m.openHealth()
		}
		return m, nil
	}
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
	case m.balance.open:
		return m.renderBalanceForm()
	case m.form.open():
		return m.renderForm()
	case m.entries.open:
		return m.renderEntryList()
	case m.picker.open():
		return m.renderPicker()
	case m.onFinances:
		return m.renderFinances()
	case m.onHealth:
		return m.renderHealth()
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
	b.WriteString(screenHead("поиск: "+m.query, fmt.Sprintf("%d из %d", len(m.visible), len(m.all))))

	if len(m.visible) == 0 {
		b.WriteString(styleDim.Render("ничего не найдено") + "\n")
		return b.String() + "\n" + styleDim.Render(m.listHint())
	}

	for i, e := range m.window() {
		idx := m.first() + i
		// Состояние приглушено, если решение по записи уже принято: канон,
		// устаревшее, тупик. Так глаз в списке из 1400 строк цепляется за то,
		// что ещё живо, а не за ровный столбец слова «active».
		state := lifecycleOf(e)
		line := fmt.Sprintf("%5d  %-9s %s", e.ID(), state, e.Title())
		if idx == m.cursor {
			b.WriteString(styleSelected.Render("▸ "+line) + "\n")
			continue
		}
		b.WriteString("  " + fmt.Sprintf("%5d  ", e.ID()) +
			lifecycleStyle(e).Render(fmt.Sprintf("%-9s", state)) + e.Title() + "\n")
	}
	if len(m.visible) > m.height {
		b.WriteString(styleDim.Render(fmt.Sprintf("\n… ещё %d", len(m.visible)-m.first()-len(m.window()))) + "\n")
	}
	return b.String() + "\n" + styleDim.Render(m.listHint())
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

// listHint names the keys this screen actually has, Tab included.
//
// Tab was missing from the line entirely while the key already worked, so the
// second screen existed and nothing on the first one said so. A key nobody is
// told about is a key nobody presses.
func (m Model) listHint() string {
	keys := hintList
	if next := m.nextScreenName(); next != "" {
		keys += " · Tab — " + next
	}
	return keys + " · Esc — выход"
}

// nextScreenName is what Tab opens from the search screen, or empty when there
// is nothing else configured.
func (m Model) nextScreenName() string {
	switch {
	case m.finances != nil:
		return "финансы"
	case m.health != nil:
		return "здоровье"
	default:
		return ""
	}
}

// lifecycleStyle dims a state that is already decided. Canonical, outdated,
// dead-end and superseded are conclusions; active is the only one still asking
// for something.
func lifecycleStyle(e domain.Entry) lipgloss.Style {
	if e.Lifecycle().IsTerminal() || e.Lifecycle().IsCanonical() {
		return styleDim
	}
	return styleTitle
}

const (
	hintList     = "печатать — искать · ↑↓ — выбор · Enter — карточка"
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
	// styleAccent — суммы и всё, что читают первым: тот же акцент, что несёт
	// заголовок экрана, чтобы глаз находил числа без подписей.
	styleAccent = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.Primary })).Bold(true)
	// styleBar и styleBarRest — заполненная и пустая часть полоски доли.
	styleBar     = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.Primary }))
	styleBarRest = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.ChartBorder }))
	styleRule    = lipgloss.NewStyle().Foreground(adaptive(func(p theme.Palette) string { return p.ChartBorder }))
)

// screenHead is the heading every screen wears: name on the left, context on
// the right, a rule under both.
//
// One function rather than each screen printing its own line — three screens
// with three slightly different headers read as three programs.
func screenHead(title, context string) string {
	head := styleQuery.Render(title)
	if context != "" {
		head += "  " + styleDim.Render(context)
	}
	return head + "\n" + styleRule.Render(strings.Repeat("─", headRule)) + "\n"
}

// bar draws the share a row takes of the largest row: filled blocks against the
// rest.
//
// Against the largest rather than against the total, on purpose: with a long
// tail every bar measured against the total is a sliver, and the column exists
// to show which rows dominate.
func bar(value, largest int64, width int) string {
	if largest <= 0 || width <= 0 {
		return ""
	}
	filled := int(value * int64(width) / largest)
	filled = max(0, min(filled, width))
	return styleBar.Render(strings.Repeat("█", filled)) +
		styleBarRest.Render(strings.Repeat("·", width-filled))
}

const (
	// headRule is how wide the rule under a heading runs. Fixed rather than the
	// terminal width: the tests drive a screen that never receives a size
	// message, and a rule collapsing to nothing there would hide regressions.
	headRule = 64
	// barWidth is the width of the share column in a breakdown.
	barWidth = 14
)

func adaptive(pick func(theme.Palette) string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: pick(theme.Light), Dark: pick(theme.Dark)}
}
