package financexlsx

import (
	"errors"
	"fmt"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// ErrIDColumnCollides is returned when the workbook keeps its ids in the column
// an account occupies whenever Источник is busy recording how the row was
// captured.
//
// Books reach that state honestly: the rule that keeps the two apart was added
// after the first pairings, and the sheet says nothing about which of the two
// meanings its eighth column carries. Every write into such a book costs a bank
// name and reports success, so the write is refused instead.
var ErrIDColumnCollides = errors.New("the id column sits where the account goes")

// idColumnCollides reports whether an expense sheet's id column is the one the
// account needs.
func idColumnCollides(idCol int) bool { return idCol == besideSourceColumn() }

// collisionError explains the state and names the command that ends it. A
// refusal that does not say how to proceed leaves the owner with a book no
// command will touch.
func collisionError(idCol int) error {
	name, err := excelize.ColumnNumberToName(idCol)
	if err != nil {
		name = fmt.Sprint(idCol)
	}
	return fmt.Errorf("%w: %s keeps ids in column %s, which is where the account goes "+
		"when Источник records how the row was captured — run `kbengine fin sync --migrate-ids` "+
		"to move them", ErrIDColumnCollides, sheetExpenses, name)
}

// MigrateIDColumn moves the ids off the account's column, header included.
//
// A book already in order is left untouched, so running this twice is not a way
// to lose a column. The same three guards as every other write apply: the lock,
// a backup, and an atomic save.
func MigrateIDColumn(path string, now func() time.Time) error {
	if err := CheckLock(path); err != nil {
		return err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	rows, err := f.GetRows(sheetExpenses, excelize.Options{RawCellValue: true})
	if err != nil {
		return fmt.Errorf("read sheet %q: %w", sheetExpenses, err)
	}
	idCol := findIDColumn(rows)
	if !idColumnCollides(idCol) {
		return nil
	}

	moves, err := planIDMoves(rows, idCol, firstFreeColumn(rows, reservedWidth(domain.KindExpense)))
	if err != nil {
		return err
	}

	if err := backup(path, now); err != nil {
		return err
	}
	for _, m := range moves {
		// SetCellStr for both ends: a ULID is a string, and letting the
		// spreadsheet guess would turn some of them into numbers or dates.
		if err := f.SetCellStr(sheetExpenses, m.to, m.value); err != nil {
			return fmt.Errorf("%s!%s: %w", sheetExpenses, m.to, err)
		}
		if err := f.SetCellStr(sheetExpenses, m.from, ""); err != nil {
			return fmt.Errorf("%s!%s: %w", sheetExpenses, m.from, err)
		}
	}
	return saveAtomically(f, path)
}

// idMove is one resolved relocation: the cell to empty, the cell to fill, and
// what goes into it.
type idMove struct {
	from, to, value string
}

// planIDMoves resolves every cell before anything is written. A workbook where
// half the ids moved is the hardest state to recover from, so the choice stays
// all or nothing — the same reason AssignIDs resolves first.
func planIDMoves(rows [][]string, from, to int) ([]idMove, error) {
	var out []idMove
	for i, row := range rows {
		rowNum := i + 1
		if rowNum < headerRow {
			continue
		}
		value := cell(row, from-1)
		if rowNum == headerRow {
			value = idHeader
		} else if value == "" {
			continue
		}
		fromCell, err := excelize.CoordinatesToCellName(from, rowNum)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", sheetExpenses, rowNum, err)
		}
		toCell, err := excelize.CoordinatesToCellName(to, rowNum)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", sheetExpenses, rowNum, err)
		}
		out = append(out, idMove{from: fromCell, to: toCell, value: value})
	}
	return out, nil
}
