// Package xlsxdim keeps a workbook's declared range honest.
//
// A worksheet announces the range it uses in <dimension ref="…">. excelize
// ignores that announcement when reading and never updates it when writing, so
// a file it has touched declares whatever was there before — for a file it
// created, the literal "A1".
//
// Readers that stream the file believe the announcement instead of scanning
// cells; openpyxl's read_only mode is the one that matters here, because it is
// what the owner and any outside script reach for on a book this size. Rows
// past the declared range are invisible to them, with no error on either side.
//
// This is the whole package on purpose: two adapters write workbooks — the
// ledger writer and the HTTP export — and a rule with two implementations is
// a rule that will eventually mean two different things.
package xlsxdim

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Sync rewrites every sheet's declared range to cover the cells it holds.
//
// The declaration is only ever widened. A book may legitimately arrive
// declaring more than it holds — a sheet whose rows were deleted by another
// program, say — and shrinking that costs a reader nothing but risks dropping
// a cell this code failed to see. Widening is the direction that fixes the
// defect; narrowing only creates chances to repeat it.
func Sync(f *excelize.File) error {
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			return fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		lastCol := 0
		for _, row := range rows {
			lastCol = max(lastCol, len(row))
		}
		lastRow := len(rows)
		if lastRow == 0 || lastCol == 0 {
			continue // an empty sheet has no range worth declaring
		}

		// Whatever the sheet already claims stays claimed, per axis.
		if declared, err := f.GetSheetDimension(sheet); err == nil && declared != "" {
			col, row, err := endOf(declared)
			if err != nil {
				return fmt.Errorf("sheet %q declares %q: %w", sheet, declared, err)
			}
			lastCol, lastRow = max(lastCol, col), max(lastRow, row)
		}

		end, err := excelize.CoordinatesToCellName(lastCol, lastRow)
		if err != nil {
			return fmt.Errorf("sheet %q: %w", sheet, err)
		}
		if err := f.SetSheetDimension(sheet, "A1:"+end); err != nil {
			return fmt.Errorf("sheet %q: %w", sheet, err)
		}
	}
	return nil
}

// endOf returns the bottom-right corner of a declared range. A single-cell
// declaration ("A1", which is what excelize writes for a new file) is its own
// corner.
func endOf(ref string) (col, row int, err error) {
	if _, end, found := strings.Cut(ref, ":"); found {
		ref = end
	}
	return excelize.CellNameToCoordinates(ref)
}
