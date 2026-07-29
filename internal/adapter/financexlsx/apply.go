package financexlsx

import (
	"fmt"
	"slices"
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
	if err := checkLock(path); err != nil {
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
	plan, err := planRowWrites(index, upserts, removals)
	if err != nil {
		return err
	}

	if err := backup(path, now); err != nil {
		return err
	}
	if err := plan.apply(f); err != nil {
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

func (p rowPlan) apply(f *excelize.File) error {
	for _, w := range p {
		if w.tx == nil {
			if err := clearRow(f, w); err != nil {
				return err
			}
			continue
		}
		if err := writeRow(f, w); err != nil {
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
func clearRow(f *excelize.File, w rowWrite) error {
	cols := append(slices.Clone(dataColumns(w.kind)), w.idCol)
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
func writeRow(f *excelize.File, w rowWrite) error {
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
		values[cols[6]] = tx.Source()
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
