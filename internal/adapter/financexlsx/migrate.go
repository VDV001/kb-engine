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
	// Moved counts the rows whose id changed column, header excluded. Zero on its
	// own does not mean the file was left alone — see Rewrote.
	Moved int
	// Rewrote reports whether the workbook was written to. A book can be rewritten
	// while Moved is zero: the header occupies the column on its own, which is
	// still a change to the file and still takes a backup.
	Rewrote bool
	// Column is where the ids live once the call returns, in spreadsheet letters.
	// For a book that has no ids yet, where they would go.
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
		// A book with no id column at all has no letter to name, and "ids are already
		// in column " is not a sentence. Where they will go once there are any is the
		// answer to the question actually being asked.
		col := idCol
		if col == 0 {
			col = firstFreeColumn(rows, reservedWidth(domain.KindExpense))
		}
		return Migration{Column: columnName(col)}, nil
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
	if err := applyIDMoves(f, moves); err != nil {
		return Migration{}, err
	}
	if err := saveAtomically(f, path); err != nil {
		return Migration{}, err
	}
	return Migration{Moved: countMovedRows(moves), Rewrote: true, Column: columnName(target)}, nil
}

// applyIDMoves writes the resolved relocations.
//
// SetCellStr for both ends: a ULID is a string, and letting the spreadsheet guess
// would turn some of them into numbers or dates.
func applyIDMoves(f *excelize.File, moves []idMove) error {
	for _, m := range moves {
		if err := f.SetCellStr(sheetExpenses, m.to, m.value); err != nil {
			return fmt.Errorf("%s!%s: %w", sheetExpenses, m.to, err)
		}
		if err := f.SetCellStr(sheetExpenses, m.from, ""); err != nil {
			return fmt.Errorf("%s!%s: %w", sheetExpenses, m.from, err)
		}
	}
	return nil
}

// countMovedRows counts the data rows among the moves.
//
// Counted rather than derived as len-1: the header is always one of the moves, and
// subtracting it made a header-only migration report zero, which reads as "nothing
// happened" about a book that had just been backed up and rewritten.
func countMovedRows(moves []idMove) int {
	n := 0
	for _, m := range moves {
		if m.value != idHeader {
			n++
		}
	}
	return n
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
// Three tests, because each one alone lets a real case through:
//
//   - the alphabet catches anything written for a person to read — Cyrillic,
//     spaces, punctuation — which is what bank names in this book look like;
//   - all-digits catches numbers, and a number is the likeliest foreign value
//     here: with RawCellValue a date arrives as its serial ("46218"), and every
//     digit is a valid id character, so the alphabet test waves it through;
//   - the accounts sheet catches a name spelled in the alphabet's own letters.
//
// ponytail: a latin word made only of these letters, containing a letter, and
// absent from the accounts sheet still passes as an id ("BANK", "SBER"). The
// upgrade path is to require the full 26-character ULID shape — deliberately not
// taken, because the fixtures across this package use short readable ids and the
// value it would buy is a case this book has never held. The two classes it does
// hold, Cyrillic names and numbers, are both caught.
//
// Not tightened by checking the ledger's own ids either: the migration is the only
// way out of a refused-write state, and a book whose ids ran ahead of the ledger
// would then have no way out at all.
func checkIsID(value string, accounts map[string]struct{}, col, row int) error {
	_, isAccount := accounts[value]
	inAlphabet := strings.IndexFunc(strings.ToUpper(value), notInIDAlphabet) < 0
	if !isAccount && inAlphabet && strings.IndexFunc(value, isNotDigit) >= 0 {
		return nil
	}
	where := columnName(col) + fmt.Sprint(row)
	return fmt.Errorf("%w: %s!%s holds %q — move it out of the id column by hand, then run this again",
		ErrIDColumnHoldsForeignData, sheetExpenses, where, value)
}

func notInIDAlphabet(r rune) bool { return !strings.ContainsRune(idAlphabet, r) }

func isNotDigit(r rune) bool { return r < '0' || r > '9' }

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
