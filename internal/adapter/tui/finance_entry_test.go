package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// stubWriter stands in for the ledger's write side. It keeps every call, so a
// test can check not only that something was written but exactly what — a form
// that drops the place or the account writes a row the owner did not describe.
type stubWriter struct {
	got []finance.AddParams
	err error
}

func (s *stubWriter) Add(p finance.AddParams) error {
	s.got = append(s.got, p)
	return s.err
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func enter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

func esc() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEsc} }

// fill types a value into the field under the cursor and moves to the next one.
func fill(m tui.Model, value string) tui.Model {
	return press(press(m, runes(value)), tab())
}

// onForm opens the finances screen and starts the entry form on it.
func onForm(m tui.Model, key string) tui.Model {
	return press(press(m, tab()), runes(key))
}

func writable() (tui.Model, *stubFinances, *stubWriter) {
	fin := &stubFinances{sum: sampleSummary()}
	w := &stubWriter{}
	return tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(w), fin, w
}

func TestFinancesWritesAnExpense(t *testing.T) {
	m, _, w := writable()

	m = onForm(m, "a")
	m = fill(m, "129,98")   // сумма
	m = fill(m, "Еда")      // категория
	m = fill(m, "Продукты") // подкатегория
	m = fill(m, "Магнит")   // место
	m = fill(m, "Сбербанк") // счёт
	m = fill(m, "по пути")  // заметка
	m = press(m, enter())

	if len(w.got) != 1 {
		t.Fatalf("ledger written %d time(s), want 1", len(w.got))
	}
	got := w.got[0]
	if got.Kind != domain.KindExpense {
		t.Errorf("kind = %q, want %q", got.Kind, domain.KindExpense)
	}
	if got.Amount.Kopecks() != 12998 {
		t.Errorf("amount = %d kopecks, want 12998", got.Amount.Kopecks())
	}
	for _, c := range []struct{ name, got, want string }{
		{"category", got.Category, "Еда"},
		{"subcategory", got.Subcategory, "Продукты"},
		{"place", got.Place, "Магнит"},
		{"account", got.Account, "Сбербанк"},
		{"description", got.Description, "по пути"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	// An omitted date is left zero rather than filled in here: today is the use
	// case's default, and a second copy of that rule is how two surfaces start
	// disagreeing about what "no date" means.
	if !got.Date.IsZero() {
		t.Errorf("date = %v, want the zero value so the use case applies today", got.Date)
	}
}

// The income form offers only what Доходы has a column for. The domain refuses
// a category, a subcategory, a place and an account on an income, so a form
// that collected them would build a row that cannot be stored.
func TestFinancesWritesAnIncome(t *testing.T) {
	m, _, w := writable()

	m = onForm(m, "i")
	m = fill(m, "90000")    // сумма
	m = fill(m, "Зарплата") // источник
	m = fill(m, "за июль")  // заметка
	m = press(m, enter())

	if len(w.got) != 1 {
		t.Fatalf("ledger written %d time(s), want 1", len(w.got))
	}
	got := w.got[0]
	if got.Kind != domain.KindIncome {
		t.Errorf("kind = %q, want %q", got.Kind, domain.KindIncome)
	}
	if got.Amount.Kopecks() != 9000000 {
		t.Errorf("amount = %d kopecks, want 9000000", got.Amount.Kopecks())
	}
	if got.Source != "Зарплата" {
		t.Errorf("source = %q, want %q", got.Source, "Зарплата")
	}
	if got.Description != "за июль" {
		t.Errorf("description = %q, want %q", got.Description, "за июль")
	}
	for _, c := range []struct{ name, value string }{
		{"category", got.Category},
		{"subcategory", got.Subcategory},
		{"place", got.Place},
		{"account", got.Account},
	} {
		if c.value != "" {
			t.Errorf("income carries %s = %q; the domain refuses it", c.name, c.value)
		}
	}
}

// The report on screen has to change with the ledger. Re-reading rather than
// adding the row to the totals in memory: after a write the screen must show
// what the file says, the rule the card edits already follow.
func TestFinancesRereadsAfterWriting(t *testing.T) {
	m, fin, _ := writable()

	m = onForm(m, "a")
	m = fill(m, "100")
	m = fill(m, "Еда")
	m = press(m, enter())

	if len(fin.asked) != 2 {
		t.Fatalf("ledger asked for a report %d time(s), want 2 (on open and after the write)", len(fin.asked))
	}
	if m.OnForm() {
		t.Error("form stayed open after a successful write")
	}
}

// A refused write must be named and must not cost the typing. Clearing the form
// on failure means the amount, the place and the note are gone by the time the
// person reads why.
func TestFinancesKeepsTheTypingWhenTheWriteFails(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	w := &stubWriter{err: errors.New("ledger is locked")}
	m := tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(w)

	m = onForm(m, "a")
	m = fill(m, "129,98")
	m = fill(m, "Еда")
	m = press(m, enter())

	view := m.View()
	if !strings.Contains(view, "ledger is locked") {
		t.Errorf("view does not name the failure\n--- view ---\n%s", view)
	}
	if !m.OnForm() {
		t.Fatal("form closed on a failed write, losing what was typed")
	}
	for _, want := range []string{"129,98", "Еда"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lost the typed %q\n--- view ---\n%s", want, view)
		}
	}
}

// An amount that does not parse is reported where it was typed, and nothing is
// written. Sending it on would put the refusal a layer away from the person who
// can fix it.
func TestFinancesRefusesAnUnparsableAmount(t *testing.T) {
	m, _, w := writable()

	m = onForm(m, "a")
	m = fill(m, "сто рублей")
	m = fill(m, "Еда")
	m = press(m, enter())

	if len(w.got) != 0 {
		t.Fatalf("ledger written %d time(s) with an unparsable amount, want 0", len(w.got))
	}
	if !m.OnForm() {
		t.Error("form closed although nothing was written")
	}
	if view := m.View(); !strings.Contains(view, "сумма") {
		t.Errorf("view does not name the amount as the problem\n--- view ---\n%s", view)
	}
}

// A date typed by hand is checked here, next to the field. The use case's
// default (today) applies only to a date nobody typed.
func TestFinancesRefusesAMalformedDate(t *testing.T) {
	m, _, w := writable()

	m = onForm(m, "a")
	m = fill(m, "100")
	m = fill(m, "Еда")
	m = fill(m, "") // подкатегория
	m = fill(m, "") // место
	m = fill(m, "") // счёт
	m = fill(m, "") // заметка
	m = fill(m, "31.07.2026")
	m = press(m, enter())

	if len(w.got) != 0 {
		t.Fatalf("ledger written %d time(s) with a malformed date, want 0", len(w.got))
	}
	if view := m.View(); !strings.Contains(view, "ГГГГ-ММ-ДД") {
		t.Errorf("view does not say what a date should look like\n--- view ---\n%s", view)
	}
}

// Esc leaves the ledger untouched. A cancelled entry that still wrote is worse
// than no cancel key at all.
func TestFinancesCancelsWithoutWriting(t *testing.T) {
	m, _, w := writable()

	m = onForm(m, "a")
	m = fill(m, "100")
	m = press(m, esc())

	if len(w.got) != 0 {
		t.Fatalf("ledger written %d time(s) after Esc, want 0", len(w.got))
	}
	if m.OnForm() {
		t.Error("Esc did not close the form")
	}
	if !m.OnFinances() {
		t.Error("Esc left the finances screen as well as the form")
	}
}

// Without a writer the keys do nothing rather than opening a form that cannot
// save — the rule the editing keys on the card already follow.
func TestFinancesOffersNoFormWithoutAWriter(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin)

	m = onForm(m, "a")

	if m.OnForm() {
		t.Fatal("form opened with no writer configured")
	}
	if view := m.View(); strings.Contains(view, "a — расход") {
		t.Errorf("read-only screen advertises a key that cannot write\n--- view ---\n%s", view)
	}
}
