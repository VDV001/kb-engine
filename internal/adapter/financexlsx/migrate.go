package financexlsx

import (
	"errors"
	"fmt"
	"strings"
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

// ErrIDColumnHoldsForeignData is returned when the column the ids sit in also
// holds something that cannot be an id.
//
// The book is then not in the state this migration repairs, and moving the cell
// anyway is how a bank name becomes the identity of its row.
var ErrIDColumnHoldsForeignData = errors.New("the id column holds something that is not an id")

// idColumnCollides reports whether an expense sheet's id column is the one the
// account needs.
func idColumnCollides(idCol int) bool { return idCol == besideSourceColumn() }

// collisionError explains the state and names the command that ends it. A
// refusal that does not say how to proceed leaves the owner with a book no
// command will touch.
func collisionError(idCol int) error {
	name := columnName(idCol)
	return fmt.Errorf("%w: %s keeps ids in column %s, which is where the account goes "+
		"when Источник records how the row was captured — run `kbengine fin sync --migrate-ids` "+
		"to move them", ErrIDColumnCollides, sheetExpenses, name)
}

// Migration says what MigrateIDColumn did. A caller that cannot tell a repaired
// book from one that needed nothing has to report one of them wrongly.
type Migration struct {
	// Moved counts the rows whose id changed column, header excluded. Zero means
	// the book was already in order.
	Moved int
	// Column is where the ids live once the call returns, in spreadsheet letters.
	Column string
}

// MigrateIDColumn moves the ids off the account's column, header included.
//
// A book already in order is left untouched, so running this twice is not a way
// to lose a column. The same three guards as every other write apply: the lock,
// a backup, and an atomic save.
func MigrateIDColumn(path string, now func() time.Time) (Migration, error) {
	if err := CheckLock(path); err != nil {
		return Migration{}, err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return Migration{}, fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	rows, err := f.GetRows(sheetExpenses, excelize.Options{RawCellValue: true})
	if err != nil {
		return Migration{}, fmt.Errorf("read sheet %q: %w", sheetExpenses, err)
	}
	idCol := findIDColumn(rows)
	if !idColumnCollides(idCol) {
		return Migration{Column: columnName(idCol)}, nil
	}

	accounts, err := knownAccounts(f, now)
	if err != nil {
		return Migration{}, err
	}
	target := firstFreeColumn(rows, reservedWidth(domain.KindExpense))
	moves, err := planIDMoves(rows, idCol, target, accounts)
	if err != nil {
		return Migration{}, err
	}

	if err := backup(path, now); err != nil {
		return Migration{}, err
	}
	for _, m := range moves {
		// SetCellStr for both ends: a ULID is a string, and letting the
		// spreadsheet guess would turn some of them into numbers or dates.
		if err := f.SetCellStr(sheetExpenses, m.to, m.value); err != nil {
			return Migration{}, fmt.Errorf("%s!%s: %w", sheetExpenses, m.to, err)
		}
		if err := f.SetCellStr(sheetExpenses, m.from, ""); err != nil {
			return Migration{}, fmt.Errorf("%s!%s: %w", sheetExpenses, m.from, err)
		}
	}
	if err := saveAtomically(f, path); err != nil {
		return Migration{}, err
	}
	// The header is one of the moves and is not a row anyone counts.
	return Migration{Moved: len(moves) - 1, Column: columnName(target)}, nil
}

// columnName renders a column for a person to read, falling back to the number
// when the spreadsheet library will not name it. A report is not worth failing
// a completed migration over.
func columnName(col int) string {
	if col == 0 {
		return ""
	}
	name, err := excelize.ColumnNumberToName(col)
	if err != nil {
		return fmt.Sprint(col)
	}
	return name
}

// idMove is one resolved relocation: the cell to empty, the cell to fill, and
// what goes into it.
type idMove struct {
	from, to, value string
}

// idAlphabet is what a generated id is made of — Crockford base32, which drops
// I, L, O and U so nothing reads as a digit it is not.
const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// checkIsID rejects a cell the migration must not move, naming it in a way the
// owner can act on.
//
// Two tests, because either one alone lets a real case through. The alphabet
// catches anything written for a person to read — Cyrillic, spaces, punctuation
// — which is what bank names in this book look like. The accounts sheet catches
// a name that happens to be spelled in the alphabet's letters.
//
// ponytail: a latin word using only these letters and absent from the accounts
// sheet still passes as an id ("BANK" would). Tightening it means requiring the
// full 26-character ULID shape, which is the upgrade path if a book ever turns
// up where that matters; today every such value in the owner's book is a bank
// name in Cyrillic, and both tests catch those.
func checkIsID(value string, accounts map[string]struct{}, col, row int) error {
	if _, isAccount := accounts[value]; !isAccount && strings.IndexFunc(strings.ToUpper(value), notInIDAlphabet) < 0 {
		return nil
	}
	where := columnName(col) + fmt.Sprint(row)
	return fmt.Errorf("%w: %s!%s holds %q — move it out of the id column by hand, then run this again",
		ErrIDColumnHoldsForeignData, sheetExpenses, where, value)
}

func notInIDAlphabet(r rune) bool { return !strings.ContainsRune(idAlphabet, r) }

// planIDMoves resolves every cell before anything is written. A workbook where
// half the ids moved is the hardest state to recover from, so the choice stays
// all or nothing — the same reason AssignIDs resolves first.
//
// A cell that cannot be an id stops the plan. This migration exists to move ids
// off a column an account needs, which means the column holds both meanings and
// the migration cannot tell them apart by position — only by looking at what is
// there. Moving a bank name into the id column costs the account and hands the
// row an identity it shares with every other row named after the same bank.
func planIDMoves(rows [][]string, from, to int, accounts map[string]struct{}) ([]idMove, error) {
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
		} else if err := checkIsID(value, accounts, from, rowNum); err != nil {
			return nil, err
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
