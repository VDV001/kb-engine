package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
)

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	panic("unknown key " + s)
}

func send(m tui.Model, keys ...string) tui.Model {
	for _, k := range keys {
		next, _ := m.Update(key(k))
		m = next.(tui.Model)
	}
	return m
}

func resize(m tui.Model, w, h int) tui.Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(tui.Model)
}

func manyEntries(t *testing.T, n int) []domain.Entry {
	t.Helper()
	out := make([]domain.Entry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, mustEntry(t, i, fmt.Sprintf("запись %d", i), "ai-agents", nil))
	}
	return out
}

// lines strips nothing — the styles are plain text under a colour code, and the
// tests look for the row content, not its colour.
func lines(view string) string { return view }

func TestModel_typingNarrowsTheList(t *testing.T) {
	m := tui.NewModel(fixture(t))

	if got := len(m.Visible()); got != 3 {
		t.Fatalf("visible = %d, want 3 before typing", got)
	}

	m = send(m, "q", "a")
	if got := len(m.Visible()); got != 1 {
		t.Fatalf("visible = %d, want 1 after typing qa", got)
	}
	if got := m.Visible()[0].ID(); got != 2 {
		t.Errorf("visible id = %d, want 2", got)
	}

	m = send(m, "backspace", "backspace")
	if got := len(m.Visible()); got != 3 {
		t.Errorf("visible = %d, want 3 after clearing the query", got)
	}
}

// Курсор, оставшийся за пределами суженного списка, показывал бы запись,
// которой на экране нет.
func TestModel_cursorStaysInsideTheList(t *testing.T) {
	m := send(tui.NewModel(fixture(t)), "down", "down")
	if got := m.Cursor(); got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}

	m = send(m, "q", "a")
	if got := m.Cursor(); got != 0 {
		t.Errorf("cursor = %d, want 0 — список сузился до одной записи", got)
	}

	m = send(m, "down", "down", "down")
	if got := m.Cursor(); got != 0 {
		t.Errorf("cursor = %d, want 0 — за последнюю запись уходить некуда", got)
	}
}

func TestModel_enterOpensTheCardAndEscReturns(t *testing.T) {
	m := send(tui.NewModel(fixture(t)), "enter")
	if !m.OnCard() {
		t.Fatal("enter did not open the card")
	}
	if got := m.View(); !strings.Contains(got, "Claude Code на автопилоте") {
		t.Errorf("card does not show the title:\n%s", got)
	}

	m = send(m, "esc")
	if m.OnCard() {
		t.Error("esc did not return to the list")
	}
}

// На карточке буквы — это уже не поиск: иначе выход из неё менял бы выдачу.
func TestModel_typingOnTheCardDoesNotSearch(t *testing.T) {
	m := send(tui.NewModel(fixture(t)), "enter", "q", "a", "esc")
	if got := len(m.Visible()); got != 3 {
		t.Errorf("visible = %d, want 3 — буквы на карточке не должны искать", got)
	}
}

func TestModel_viewShowsWhatWasNotFound(t *testing.T) {
	m := send(tui.NewModel(fixture(t)), "z", "z", "z")
	if got := m.View(); !strings.Contains(got, "ничего не найдено") {
		t.Errorf("empty result is silent:\n%s", got)
	}
}

// Каталог длиннее экрана — это норма, а не край: в живой базе 1340 записей при
// окне в 15 строк. Окно обязано ехать за курсором, иначе выбранной записи не
// видно ровно тогда, когда список стал интересным.
func TestModel_windowFollowsTheCursor(t *testing.T) {
	m := tui.NewModel(manyEntries(t, 100))
	m = resize(m, 80, 16) // 16 - 6 служебных строк = окно в 10

	if got := lines(m.View()); !strings.Contains(got, "    1") {
		t.Fatalf("первая запись не видна:\n%s", got)
	}
	for range 20 {
		m = send(m, "down")
	}
	view := m.View()
	if !strings.Contains(view, "   21") {
		t.Errorf("курсор ушёл за окно — выбранной записи не видно:\n%s", view)
	}
	if strings.Contains(view, "    1 ") {
		t.Errorf("окно не поехало: первая запись всё ещё на экране:\n%s", view)
	}
	if !strings.Contains(view, "… ещё") {
		t.Error("не сказано, сколько записей осталось за экраном")
	}
}

// Экран меньше трёх строк — это уже не список; окно не должно схлопнуться в
// ноль и уронить срез.
func TestModel_survivesATinyTerminal(t *testing.T) {
	m := resize(tui.NewModel(manyEntries(t, 20)), 40, 1)
	if got := m.View(); got == "" {
		t.Error("пустой вид на крошечном терминале")
	}
}

func TestModel_ctrlCQuitsFromBothScreens(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"из списка", nil},
		{"с карточки", []string{"enter"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := send(tui.NewModel(fixture(t)), tc.keys...)
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if cmd == nil {
				t.Fatal("Ctrl+C не завершает программу")
			}
			if msg := cmd(); msg != tea.Quit() {
				t.Errorf("команда = %T, want tea.Quit", msg)
			}
		})
	}
}

// Вердикт есть не у всех: у непрочитанного он появляется только после разбора,
// и карточка должна показывать состояние чтения, а не пустоту.
func TestModel_cardFallsBackToReadState(t *testing.T) {
	m := send(tui.NewModel(fixture(t)), "enter")
	if got := m.View(); !strings.Contains(got, "read") {
		t.Errorf("карточка не показала состояние чтения:\n%s", got)
	}
}

// Экран обязан помещаться в окно целиком. Одна лишняя строка стоит верхней:
// терминал прокручивает вывод, и строка поиска — то единственное, что говорит,
// что именно набрано, — уезжает за верхний край.
//
// Проверяется на длинном списке, потому что дефект живёт именно там: когда
// найденного больше, чем влезает, снизу добавляется блок «… ещё N», которого
// при коротком результате нет. Поэтому же баг и выглядел плавающим — при двух
// найденных записях всё было видно.
func TestModel_viewFitsTheWindow(t *testing.T) {
	for _, c := range []struct {
		name          string
		height, found int
	}{
		{"низкое окно, длинный список", 24, 1000},
		{"обычное окно, длинный список", 40, 1000},
		{"крошечное окно", 10, 1000},
		{"список короче окна", 24, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := resize(tui.NewModel(manyEntries(t, c.found)), 100, c.height)

			got := strings.Count(m.View(), "\n") + 1
			if got > c.height {
				t.Errorf("экран занимает %d строк при окне %d — верх уедет за край\n--- view ---\n%s",
					got, c.height, m.View())
			}
		})
	}
}

// Строка поиска — первая строка экрана, и она обязана остаться видимой на
// длинном списке: без неё не видно, что набрано.
func TestModel_queryStaysOnScreen(t *testing.T) {
	m := resize(tui.NewModel(manyEntries(t, 1000)), 100, 24)
	m = send(m, "з")

	view := m.View()
	first, _, _ := strings.Cut(view, "\n")
	if !strings.Contains(first, "поиск: з") {
		t.Errorf("первая строка экрана = %q, ожидалась строка поиска", first)
	}
	if n := strings.Count(view, "\n") + 1; n > 24 {
		t.Errorf("экран %d строк при окне 24 — первая строка уедет вверх", n)
	}
}
