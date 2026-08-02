package tui

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
)

// EntryLoader is what the screen needs from the catalog: the entries. It lives
// here, in the consumer, rather than in the domain — the reader declares the
// shape of what it consumes.
type EntryLoader interface {
	Entries() ([]domain.Entry, error)
}

// Run loads the catalog and hands the terminal to the search screen. It returns
// after the user quits.
//
// The saver may be nil, and then the screen is read-only: it offers no editing
// keys at all rather than keys that quietly do nothing. The same holds for fin
// and ledger: nil means no ledger is configured, so the finances key is simply
// absent, and a ledger that can be read but not written keeps the entry keys
// off the same way.
func Run(loader EntryLoader, saver EntrySaver, fin FinanceLoader, ledger FinanceWriter, in io.Reader, out io.Writer) error {
	entries, err := loader.Entries()
	if err != nil {
		return fmt.Errorf("load catalog: %w", err)
	}
	model := NewModel(entries)
	if saver != nil {
		model = NewEditableModel(entries, saver, loader)
	}
	if fin != nil {
		model = model.WithFinances(fin)
	}
	if ledger != nil {
		model = model.WithFinanceWriter(ledger)
	}
	p := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
