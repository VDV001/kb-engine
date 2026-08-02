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

// Sources is what the screen may reach. Every field but Entries is optional,
// and a nil one means the keys that need it are absent rather than present and
// inert — the rule this screen follows everywhere.
//
// Named fields rather than positional arguments: five dependencies read as five
// nils at the call site, and the next source would make it six.
type Sources struct {
	Entries  EntryLoader
	Saver    EntrySaver
	Finances FinanceLoader
	Ledger   FinanceWriter
	Workbook WorkbookSyncer
	Words    VocabularySource
	Accounts AccountsSource
}

// Run loads the catalog and hands the terminal to the search screen. It returns
// after the user quits.
func Run(s Sources, in io.Reader, out io.Writer) error {
	entries, err := s.Entries.Entries()
	if err != nil {
		return fmt.Errorf("load catalog: %w", err)
	}
	model := NewModel(entries)
	if s.Saver != nil {
		model = NewEditableModel(entries, s.Saver, s.Entries)
	}
	if s.Finances != nil {
		model = model.WithFinances(s.Finances)
	}
	if s.Ledger != nil {
		model = model.WithFinanceWriter(s.Ledger)
	}
	if s.Workbook != nil {
		model = model.WithWorkbookSyncer(s.Workbook)
	}
	if s.Words != nil {
		model = model.WithVocabulary(s.Words)
	}
	if s.Accounts != nil {
		model = model.WithAccounts(s.Accounts)
	}
	p := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
