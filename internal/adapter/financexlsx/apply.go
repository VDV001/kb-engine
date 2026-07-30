package financexlsx

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// dataColumns lists the 1-based columns a transaction occupies, in the order
// the sheet keeps them.
func dataColumns(kind string) []int {
	if kind == domain.KindExpense {
		// Дата | Категория | Подкатегория | Место | Описание | Сумма | Источник
		return []int{1, 2, 3, 4, 5, 6, 7}
	}
	// Дата | Источник | Описание | Сумма
	return []int{1, 2, 3, 4}
}

// besideSourceColumn is the unlabelled column immediately after the documented
// ones, where the live ledger keeps the account for rows whose Источник records
// how the record was captured instead.
func besideSourceColumn() int { return len(dataColumns(domain.KindExpense)) + 1 }

// reservedWidth is how many columns a sheet's write contract owns: the
// documented ones, plus — for Расходы — the column beside Источник, which is
// where the account goes when Источник is busy recording how the row was
// captured.
//
// The id column is chosen from past this, so the two can never land on the same
// cell. They used to be decided by unrelated rules: on a book whose eighth
// column happened to be empty both picked 8, and the id overwrote the account
// in the same pass.
func reservedWidth(kind string) int {
	if kind == domain.KindExpense {
		return besideSourceColumn()
	}
	return len(dataColumns(kind))
}

// placeSourceAndAccount decides the two cells that carry where the money was
// and how the row was captured.
//
// Источник carries the account unless the row records a capture method, in
// which case the account goes to the column beside it. That is the arrangement
// the sheet already uses, so writing a row back unchanged leaves the file alone.
//
// Both outcomes are written, not only the one that adds. An account removed
// through the ledger has to disappear from the sheet too: Fingerprint covers
// Account, so a cell left behind makes the next sync read the workbook as
// changed and pull the old value back, undoing the deletion. Clearing stays
// limited to a cell holding one of our own account names — anything else there
// is the owner's.
//
// Whether the cell beside Источник has to be cleared depends on the account
// alone, not on the source. Deciding it inside the branch for rows that have a
// source left the other branch to return early, so a row that lost both values
// kept its bank in the column next door and the next sync read the deletion as
// a change and put it back.
func placeSourceAndAccount(f *excelize.File, w rowWrite, tx domain.Transaction,
	sourceCol int, accounts map[string]struct{}, values map[int]any,
) error {
	if tx.Source() == "" {
		values[sourceCol] = tx.Account()
	} else {
		values[sourceCol] = tx.Source()
		if tx.Account() != "" {
			values[besideSourceColumn()] = tx.Account()
			return nil
		}
	}

	// The account is not going beside Источник — either it is empty or it fits
	// in Источник itself — so whatever of ours sits there is stale.
	ours, err := holdsAnAccount(f, w.sheet, besideSourceColumn(), w.row, accounts)
	if err != nil {
		return err
	}
	if ours {
		values[besideSourceColumn()] = ""
	}
	return nil
}

// ErrUnknownAccount is returned when a row carries an account the Счета sheet
// does not list.
//
// That sheet is the vocabulary the reader uses to tell an account from a source,
// so a name missing from it cannot be read back as an account — it returns as a
// source, or not at all.
var ErrUnknownAccount = errors.New("the workbook does not know this account")

// ErrSourceNamesAnAccount is returned when a row's source is spelled like one of
// the known accounts.
//
// The reader would take it for the account, dropping the source. Which of the
// two was meant is not something this layer can decide.
var ErrSourceNamesAnAccount = errors.New("the source names a known account")

// checkVocabulary rejects rows the workbook cannot store without changing their
// meaning.
//
// The check lives here rather than in the domain on purpose: the domain leaves
// the set of accounts open — the sheet lists five and a closed set would reject
// the sixth on the day one is opened — while this workbook can only store what
// its Счета sheet names. That is a property of the storage, so it is enforced at
// the boundary, before anything is written.
func checkVocabulary(txs []domain.Transaction, accounts map[string]struct{}) error {
	for _, tx := range txs {
		if acc := tx.Account(); acc != "" {
			if _, known := accounts[acc]; !known {
				return fmt.Errorf("%w: %q — add it to the Счета sheet, then run this again (row %s)",
					ErrUnknownAccount, acc, tx.ID())
			}
		}
		if src := tx.Source(); src != "" {
			if _, isAccount := accounts[src]; isAccount {
				return fmt.Errorf("%w: %q is on the Счета sheet, so it would read back as the account — "+
					"pass it as the account instead (row %s)", ErrSourceNamesAnAccount, src, tx.ID())
			}
		}
	}
	return nil
}

// holdsAnAccount reports whether a cell contains one of the workbook's own
// account names. Anything else there belongs to the owner, and neither writing
// nor clearing a row may touch it.
func holdsAnAccount(f *excelize.File, sheet string, col, row int, accounts map[string]struct{}) (bool, error) {
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return false, fmt.Errorf("%s row %d: %w", sheet, row, err)
	}
	v, err := f.GetCellValue(sheet, name)
	if err != nil {
		return false, fmt.Errorf("%s!%s: %w", sheet, name, err)
	}
	_, ok := accounts[strings.TrimSpace(v)]
	return ok, nil
}

// sheetIndex is what one sheet looks like before anything is written.
type sheetIndex struct {
	idCol   int
	rowByID map[string]int
	lastRow int
	// lastDataRow is the last row that actually holds a transaction. The live
	// sheet reports 1156 rows for 507 records, because the rows past the data
	// carry formatting; inheriting from those gives a new row a border and no
	// currency format.
	lastDataRow int
}

// ApplyRows writes transaction changes into the workbook: upserts are matched
// by id and appended when the workbook has never seen them, removals clear
// their row.
//
// Removal clears rather than deletes. Deleting a row shifts every row below it,
// which moves data out from under the formulas on Сводка; a blank row mid-sheet
// is something this ledger already contains and the reader already skips.
//
// Every change is resolved before anything is written, and the same three
// guards as AssignIDs apply: the lock, a backup, and an atomic save.
func ApplyRows(path string, upserts []domain.Transaction, removals []string, now func() time.Time) error {
	if err := CheckLock(path); err != nil {
		return err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	index, err := indexSheets(f)
	if err != nil {
		return err
	}
	// Refused for the whole book, not only the rows in this call: a sheet whose
	// ids sit on the account's column reads wrong as well as writes wrong, so
	// there is no part of it that is safe to touch meanwhile.
	if idCol := index[sheetExpenses].idCol; idColumnCollides(idCol) {
		return collisionError(idCol)
	}
	accounts, err := knownAccounts(f, now)
	if err != nil {
		return err
	}
	if err := checkVocabulary(upserts, accounts); err != nil {
		return err
	}
	plan, err := planRowWrites(index, upserts, removals)
	if err != nil {
		return err
	}

	if err := backup(path, now); err != nil {
		return err
	}
	if err := plan.apply(f, accounts); err != nil {
		return err
	}
	return saveAtomically(f, path)
}

// rowWrite is one resolved change. tx is nil for a removal.
type rowWrite struct {
	sheet     string
	row       int
	styleFrom int // row to copy formatting from; 0 means leave formatting alone
	tx        *domain.Transaction
	idCol     int
	kind      string
}

// kindOf maps a sheet back to the kind of transaction it holds, so a removal
// knows how wide its row is.
func kindOf(sheet string) string {
	if sheet == sheetIncome {
		return domain.KindIncome
	}
	return domain.KindExpense
}

type rowPlan []rowWrite

func indexSheets(f *excelize.File) (map[string]sheetIndex, error) {
	out := map[string]sheetIndex{}
	for _, sheet := range []string{sheetExpenses, sheetIncome} {
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		idx := sheetIndex{idCol: findIDColumn(rows), rowByID: map[string]int{}, lastRow: len(rows)}
		if idx.idCol != 0 {
			for i, row := range rows {
				rowNum := i + 1
				if rowNum < firstDataRow {
					continue
				}
				if id := cell(row, idx.idCol-1); id != "" {
					idx.rowByID[id] = rowNum
					idx.lastDataRow = max(idx.lastDataRow, rowNum)
				}
			}
		}
		out[sheet] = idx
	}
	return out, nil
}

// planRowWrites resolves every change to a cell range, failing before anything
// is written if a change cannot be placed.
func planRowWrites(index map[string]sheetIndex, upserts []domain.Transaction, removals []string) (rowPlan, error) {
	var plan rowPlan

	for _, id := range removals {
		sheet, row, ok := locate(index, id)
		if !ok {
			return nil, fmt.Errorf("cannot remove %q: the workbook has no row with that id", id)
		}
		plan = append(plan, rowWrite{sheet: sheet, row: row, idCol: index[sheet].idCol, kind: kindOf(sheet)})
	}

	for _, tx := range upserts {
		sheet := sheetExpenses
		if !tx.IsExpense() {
			sheet = sheetIncome
		}
		idx := index[sheet]
		if idx.idCol == 0 {
			return nil, fmt.Errorf("sheet %q has no id column — run `fin sync --init` first", sheet)
		}
		row, known := idx.rowByID[tx.ID()]
		styleFrom := 0
		if !known {
			// Append below the last row, and take the formatting with it: a date
			// rendered as 46110 and an amount with no currency read as a broken file.
			styleFrom = idx.lastDataRow
			idx.lastRow++
			row = idx.lastRow
			idx.rowByID[tx.ID()] = row
			index[sheet] = idx
		}
		plan = append(plan, rowWrite{sheet: sheet, row: row, styleFrom: styleFrom, tx: &tx, idCol: idx.idCol})
	}

	// Deterministic order, so the same change produces the same file.
	slices.SortStableFunc(plan, func(a, b rowWrite) int {
		if a.sheet != b.sheet {
			if a.sheet < b.sheet {
				return -1
			}
			return 1
		}
		return a.row - b.row
	})
	return plan, nil
}

func locate(index map[string]sheetIndex, id string) (sheet string, row int, ok bool) {
	for _, name := range []string{sheetExpenses, sheetIncome} {
		if row, ok = index[name].rowByID[id]; ok {
			return name, row, true
		}
	}
	return "", 0, false
}

// knownAccounts is the vocabulary from the Счета sheet, used to tell an account
// sitting beside Источник from a note the owner keeps there.
func knownAccounts(f *excelize.File, now func() time.Time) (map[string]struct{}, error) {
	accs, err := readAccounts(f, now)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(accs))
	for _, a := range accs {
		out[a.Bank()] = struct{}{}
	}
	return out, nil
}

func (p rowPlan) apply(f *excelize.File, accounts map[string]struct{}) error {
	for _, w := range p {
		if w.tx == nil {
			if err := clearRow(f, w, accounts); err != nil {
				return err
			}
			continue
		}
		if err := writeRow(f, w, accounts); err != nil {
			return err
		}
	}
	return nil
}

// clearRow blanks the data cells and the id, leaving the row in place.
//
// Only the columns the sheet actually uses. Доходы is four wide, and blanking
// seven would reach past it into whatever the owner keeps out there — the same
// mistake the id column was placed to avoid.
func clearRow(f *excelize.File, w rowWrite, accounts map[string]struct{}) error {
	cols := append(slices.Clone(dataColumns(w.kind)), w.idCol)
	// The account may be in the column beside Источник. Clear it only when it is
	// one — anything else there is the owner's note, not ours to remove.
	if w.kind == domain.KindExpense {
		ours, err := holdsAnAccount(f, w.sheet, besideSourceColumn(), w.row, accounts)
		if err != nil {
			return err
		}
		if ours {
			cols = append(cols, besideSourceColumn())
		}
	}
	for _, col := range cols {
		name, err := excelize.CoordinatesToCellName(col, w.row)
		if err != nil {
			return fmt.Errorf("%s row %d: %w", w.sheet, w.row, err)
		}
		if err := f.SetCellStr(w.sheet, name, ""); err != nil {
			return fmt.Errorf("%s!%s: %w", w.sheet, name, err)
		}
	}
	return nil
}

// writeRow puts a transaction into its row, preserving the formatting that was
// already there and inheriting it from the row above for a freshly appended one.
func writeRow(f *excelize.File, w rowWrite, accounts map[string]struct{}) error {
	tx := *w.tx
	cols := dataColumns(tx.Kind())

	styleFrom := w.styleFrom
	if styleFrom == 0 {
		styleFrom = w.row
	}
	styles, err := rowStyles(f, w.sheet, styleFrom, append(slices.Clone(cols), w.idCol))
	if err != nil {
		return err
	}

	values := map[int]any{cols[0]: tx.Date()}
	if tx.IsExpense() {
		values[cols[1]] = tx.Category()
		values[cols[2]] = tx.Subcategory()
		values[cols[3]] = tx.Place()
		values[cols[4]] = tx.Description()
		values[cols[5]] = float64(tx.Amount().Kopecks()) / 100
		if err := placeSourceAndAccount(f, w, tx, cols[6], accounts, values); err != nil {
			return err
		}
	} else {
		values[cols[1]] = tx.Source()
		values[cols[2]] = tx.Description()
		values[cols[3]] = float64(tx.Amount().Kopecks()) / 100
	}

	for col, v := range values {
		name, err := excelize.CoordinatesToCellName(col, w.row)
		if err != nil {
			return fmt.Errorf("%s row %d: %w", w.sheet, w.row, err)
		}
		if err := f.SetCellValue(w.sheet, name, v); err != nil {
			return fmt.Errorf("%s!%s: %w", w.sheet, name, err)
		}
	}
	idCell, err := excelize.CoordinatesToCellName(w.idCol, w.row)
	if err != nil {
		return fmt.Errorf("%s row %d: %w", w.sheet, w.row, err)
	}
	if err := f.SetCellStr(w.sheet, idCell, tx.ID()); err != nil {
		return fmt.Errorf("%s!%s: %w", w.sheet, idCell, err)
	}

	// Styles last: writing a time.Time applies a date format of its own, and the
	// sheet's formatting has to win over that.
	return applyStyles(f, w.sheet, w.row, styles)
}

func rowStyles(f *excelize.File, sheet string, row int, cols []int) (map[int]int, error) {
	out := make(map[int]int, len(cols))
	for _, col := range cols {
		name, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", sheet, row, err)
		}
		id, err := f.GetCellStyle(sheet, name)
		if err != nil {
			return nil, fmt.Errorf("%s!%s: %w", sheet, name, err)
		}
		out[col] = id
	}
	return out, nil
}

func applyStyles(f *excelize.File, sheet string, row int, styles map[int]int) error {
	for col, id := range styles {
		if id == 0 {
			continue
		}
		name, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return fmt.Errorf("%s row %d: %w", sheet, row, err)
		}
		if err := f.SetCellStyle(sheet, name, name, id); err != nil {
			return fmt.Errorf("%s!%s: %w", sheet, name, err)
		}
	}
	return nil
}
