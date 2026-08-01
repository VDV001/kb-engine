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
func Run(loader EntryLoader, in io.Reader, out io.Writer) error {
	entries, err := loader.Entries()
	if err != nil {
		return fmt.Errorf("load catalog: %w", err)
	}
	p := tea.NewProgram(NewModel(entries), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
