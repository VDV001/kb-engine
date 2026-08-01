package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/adapter/tui"
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
