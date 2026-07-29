package financexlsx_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// A book paired before the placement rule existed keeps its ids in the column
// an account uses when Источник is busy. Writing into it costs a bank name per
// row and says nothing, so the write is refused instead — losing the change is
// recoverable, losing the account silently is not.
func TestApplyRows_refusesABookWhoseIDColumnHoldsTheAccount(t *testing.T) {
	path := pairedByOlderEngine(t)

	err := financexlsx.ApplyRows(path,
		[]domain.Transaction{txWith(t, "01B", "Т-Банк", "Чек")}, nil, writeClock)
	if !errors.Is(err, financexlsx.ErrIDColumnCollides) {
		t.Fatalf("ApplyRows error = %v, want ErrIDColumnCollides", err)
	}
	// The refusal has to name the way out; an error that only says no leaves the
	// owner with a book no command will touch.
	if got := err.Error(); !strings.Contains(got, "--migrate-ids") {
		t.Errorf("error %q does not say how to fix it", got)
	}

	// Refused before anything moved: the ids are where they were, and no backup
	// was taken, because nothing was going to be written.
	if got := cellValue(t, path, "Расходы", "H3"); got != "01A" {
		t.Errorf("Расходы!H3 = %q after a refused write, want 01A", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".backup")); !os.IsNotExist(err) {
		t.Error("a refused write took a backup")
	}
}

// The migration moves the ids past the account's column and takes the header
// with them, so the column explains itself afterwards.
func TestMigrateIDColumn_movesIDsPastTheAccountColumn(t *testing.T) {
	path := pairedByOlderEngine(t)

	if _, err := financexlsx.MigrateIDColumn(path, writeClock); err != nil {
		t.Fatalf("MigrateIDColumn: %v", err)
	}

	for cell, want := range map[string]string{
		"I2": "id", "I3": "01A", "I4": "01B",
		"H2": "", "H3": "", "H4": "",
	} {
		if got := cellValue(t, path, "Расходы", cell); got != want {
			t.Errorf("Расходы!%s = %q, want %q", cell, got, want)
		}
	}

	// Identity survives the move: the ledger matches rows by id, and a migration
	// that renumbers them would orphan every record on the other side.
	got := readBack(t, path)
	for _, id := range []string{"01A", "01B", "01C"} {
		if _, ok := got[id]; !ok {
			t.Errorf("row %s lost its id in the migration", id)
		}
	}
}

// A book that is already in order is left alone, so running the migration twice
// is not a way to lose a column.
func TestMigrateIDColumn_leavesAWellPlacedBookAlone(t *testing.T) {
	path := paired(t)
	before := map[string]string{}
	for _, cell := range []string{"G3", "H3", "I3", "G4", "H4", "I4"} {
		before[cell] = cellValue(t, path, "Расходы", cell)
	}

	if _, err := financexlsx.MigrateIDColumn(path, writeClock); err != nil {
		t.Fatalf("MigrateIDColumn: %v", err)
	}

	for cell, want := range before {
		if got := cellValue(t, path, "Расходы", cell); got != want {
			t.Errorf("Расходы!%s = %q, want %q — nothing to migrate", cell, got, want)
		}
	}
}
