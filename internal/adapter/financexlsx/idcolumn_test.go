package financexlsx_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/xuri/excelize/v2"
)

// pairedByOlderEngineWithABankName is the colliding book with a row added by
// hand afterwards: the owner filled the eighth column the way the rest of the
// book uses it, with the name of an account, not knowing the engine had claimed
// that column for ids.
func pairedByOlderEngineWithABankName(t *testing.T) string {
	t.Helper()
	path := pairedByOlderEngine(t)

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if err := f.SetCellStr("Расходы", "H4", "Сбербанк"); err != nil {
		t.Fatalf("Расходы!H4: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

// ApplyRows refuses a colliding book. AssignIDs writes into the same column of
// the same book and did not ask, so the guard covered one writer out of the
// three that reach that column — and the way to reach AssignIDs is ordinary:
// `fin sync --init` against a ledger path that does not exist yet.
//
// What it costs: a ULID written over a bank name, backup taken, success
// reported.
func TestAssignIDs_refusesABookWhoseIDColumnHoldsTheAccount(t *testing.T) {
	path := pairedByOlderEngineWithABankName(t)

	err := financexlsx.AssignIDs(path, map[string]string{"expense-r5": "01D"}, writeClock)
	if !errors.Is(err, financexlsx.ErrIDColumnCollides) {
		t.Fatalf("AssignIDs error = %v, want ErrIDColumnCollides", err)
	}
	if got := cellValue(t, path, "Расходы", "H4"); got != "Сбербанк" {
		t.Errorf("Расходы!H4 = %q after a refused write, want Сбербанк", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".backup")); !os.IsNotExist(err) {
		t.Error("a refused write took a backup")
	}
}

// Moved is what the CLI decides between "nothing to move" and a report of work,
// so a wrong count is a wrong story about a file that was rewritten.
//
// Two ways it lied. A book whose only occupant of the column is the header gets
// backed up and rewritten, and reports "nothing to move" — Moved is len(moves)-1
// and the header is one of the moves. A book with no id column at all reports
// "ids are already in column " with the letter missing.
func TestMigrateIDColumn_reportsWhatItDid(t *testing.T) {
	t.Run("header only", func(t *testing.T) {
		path := pairedByOlderEngine(t)
		f, err := excelize.OpenFile(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		// Ids gone, header left behind — the state a book reaches when its rows were
		// cleared by hand.
		for _, cell := range []string{"H3", "H4"} {
			if err := f.SetCellStr("Расходы", cell, ""); err != nil {
				t.Fatalf("clear %s: %v", cell, err)
			}
		}
		if err := f.Save(); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		m, err := financexlsx.MigrateIDColumn(path, writeClock)
		if err != nil {
			t.Fatalf("MigrateIDColumn: %v", err)
		}
		// The header did move, so this is not "nothing to move".
		if got := cellValue(t, path, "Расходы", "I2"); got != "id" {
			t.Fatalf("Расходы!I2 = %q, want the header to have moved", got)
		}
		// Moved counts rows, and no row carried an id — so the honest report is
		// Moved=0 with Rewrote=true, not a fabricated count of one.
		if m.Moved != 0 {
			t.Errorf("Moved = %d, want 0 — no row carried an id", m.Moved)
		}
		if !m.Rewrote {
			t.Error("Rewrote = false for a book that was backed up and written to")
		}
		if m.Column != "I" {
			t.Errorf("Column = %q, want I", m.Column)
		}
	})

	t.Run("no id column at all", func(t *testing.T) {
		path := workbookWithoutExtraColumn(t)

		m, err := financexlsx.MigrateIDColumn(path, writeClock)
		if err != nil {
			t.Fatalf("MigrateIDColumn: %v", err)
		}
		if m.Moved != 0 {
			t.Errorf("Moved = %d for a book with no ids, want 0", m.Moved)
		}
		// "ids are already in column " with nothing after it is not a sentence.
		if m.Column == "" {
			t.Error("Column is empty — the report has a hole where the column goes")
		}
	})
}

// A date read with RawCellValue is a serial number — "46218" is how the reader
// itself receives 30.07.2026 (see parseDate). A number in the id column is
// therefore the likeliest foreign value in this book, more likely than any word,
// and the alphabet test alone accepted every one of them: digits are all valid
// id characters.
//
// Accepted as an id, a serial date becomes the identity of its row, the value it
// held is gone, and two rows carrying the same number produce a duplicate id that
// fails every later sync.
func TestMigrateIDColumn_refusesANumberAsAnID(t *testing.T) {
	for _, value := range []string{"46218", "2026", "500", "0"} {
		t.Run(value, func(t *testing.T) {
			path := pairedByOlderEngine(t)
			f, err := excelize.OpenFile(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if err := f.SetCellStr("Расходы", "H4", value); err != nil {
				t.Fatalf("set H4: %v", err)
			}
			if err := f.Save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			_, err = financexlsx.MigrateIDColumn(path, writeClock)
			if !errors.Is(err, financexlsx.ErrIDColumnHoldsForeignData) {
				t.Fatalf("MigrateIDColumn with %q in the id column = %v, want ErrIDColumnHoldsForeignData", value, err)
			}
			if got := cellValue(t, path, "Расходы", "H4"); got != value {
				t.Errorf("Расходы!H4 = %q after a refused migration, want %q", got, value)
			}
		})
	}
}

// The migration moved whatever it found, so the command offered as the way out
// of a collision could make the book worse: a bank name became the identity of
// its row, and the account it named was gone. Two such rows read back as one
// duplicate id, which fails every later sync until someone repairs the file by
// hand.
//
// A cell that cannot be an id means the book is not in the state this migration
// repairs, so it declines and says which cell to look at.
func TestMigrateIDColumn_refusesToMoveWhatIsNotAnID(t *testing.T) {
	path := pairedByOlderEngineWithABankName(t)

	_, err := financexlsx.MigrateIDColumn(path, writeClock)
	if !errors.Is(err, financexlsx.ErrIDColumnHoldsForeignData) {
		t.Fatalf("MigrateIDColumn error = %v, want ErrIDColumnHoldsForeignData", err)
	}
	// The refusal has to be actionable: the owner has one cell to look at, and the
	// message is the only place that says which.
	if got := err.Error(); !strings.Contains(got, "H4") || !strings.Contains(got, "Сбербанк") {
		t.Errorf("error %q names neither the cell nor what is in it", got)
	}

	// Nothing moved and nothing was copied: refused before the first mutation.
	for cell, want := range map[string]string{"H3": "01A", "H4": "Сбербанк", "I3": "", "I4": ""} {
		if got := cellValue(t, path, "Расходы", cell); got != want {
			t.Errorf("Расходы!%s = %q after a refused migration, want %q", cell, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".backup")); !os.IsNotExist(err) {
		t.Error("a refused migration took a backup")
	}
}
