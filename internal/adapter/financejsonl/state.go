package financejsonl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// LoadState reads the baseline the two sides last agreed on.
//
// A missing file is not an error, and this is the one place in the package
// where absence is legitimate: the ledger going missing means data vanished,
// while a baseline going missing means the sides have never been synced. Diff
// already refuses to move anything until they agree.
//
// A file that cannot be parsed is a different matter — something wrote nonsense
// where the baseline should be, and reporting "never synced" would quietly
// discard the record of what the sides last agreed on.
func LoadState(path string) (finance.SyncState, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return finance.SyncState{}, nil
	}
	if err != nil {
		return finance.SyncState{}, fmt.Errorf("open sync state: %w", err)
	}

	var st finance.SyncState
	if err := json.Unmarshal(raw, &st); err != nil {
		return finance.SyncState{}, fmt.Errorf("decode sync state %s: %w", path, err)
	}
	return st, nil
}

// SaveState records the baseline after a successful sync.
func SaveState(path string, st finance.SyncState) error {
	return writeAtomically(path, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		// Indented: the file is small, and it gets read by a person when a sync
		// goes sideways.
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(st); err != nil {
			return fmt.Errorf("encode sync state: %w", err)
		}
		return nil
	})
}
