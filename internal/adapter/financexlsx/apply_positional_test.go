package financexlsx_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// halfPaired builds the state the live workbook is actually in: the id column
// exists, but not every row carries an id. Rows imported before the column was
// added have an empty cell, and the reader hands those a positional id.
//
// This is not a contrived shape: a book kept by hand before the id column
// existed carries such rows for every day that was imported without one.
func halfPaired(t *testing.T) string {
	t.Helper()
	path := workbookWithExtraColumn(t)
	// Expenses row 4 is left without an id on purpose; the reader will call it
	// "expense-r4".
	if err := financexlsx.AssignIDs(path, map[string]string{
		"expense-r3": "01A", "income-r3": "01C",
	}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}
	return path
}

// editDescription reads a row back out of the workbook and returns it with one
// field changed — the same round trip `fin edit` makes, so the test cannot be
// green against a transaction the real flow never builds.
func editDescription(t *testing.T, path, id, note string) domain.Transaction {
	t.Helper()
	was, ok := readBack(t, path)[id]
	if !ok {
		t.Fatalf("row %q is not in the workbook to begin with", id)
	}
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:          was.ID(),
		Kind:        was.Kind(),
		Date:        was.Date(),
		Amount:      was.Amount(),
		Category:    was.Category(),
		Subcategory: was.Subcategory(),
		Place:       was.Place(),
		Description: note,
		Source:      was.Source(),
		Account:     was.Account(),
		Now:         writeClock,
	})
	if err != nil {
		t.Fatalf("rebuild transaction: %v", err)
	}
	return out
}

// rowCount counts what the reader actually finds, row by row.
//
// Not readBack: that keys by id, and the defect under test produces two rows
// carrying the SAME id — the map collapses them and reports the healthy count.
// The first version of this test passed against the unfixed writer for exactly
// that reason. A duplicate has to be counted where it exists, in the sheet.
func rowCount(t *testing.T, path string) int {
	t.Helper()
	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return len(led.Transactions)
}

// A row the reader identifies positionally must be updated in place, not
// appended.
//
// The reader and the writer have to agree on what a row is called. When they
// disagree the sync cannot find the row it means to change, treats the record as
// new, and writes the transaction a second time — the workbook then holds one
// expense twice and any sum over the column is wrong by that amount.
func TestApplyRows_updatesRowWhoseIDCellIsEmpty(t *testing.T) {
	path := halfPaired(t)
	before := rowCount(t, path)

	// Same row, one field changed — exactly what `fin edit` produces.
	changed := editDescription(t, path, "expense-r4", "подкатегорию поправили")

	if err := financexlsx.ApplyRows(path, []domain.Transaction{changed}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	if after := rowCount(t, path); after != before {
		t.Fatalf("rows %d → %d — updating a positionally identified row must not add one", before, after)
	}
	row, ok := readBack(t, path)["expense-r4"]
	if !ok {
		t.Fatalf("row expense-r4 did not come back")
	}
	if row.Description() != "подкатегорию поправили" {
		t.Errorf("description = %q, want the edit to have landed", row.Description())
	}
}

// Having matched a row positionally, the write stores the id in the cell. The
// next read then finds it by id, so the row leaves the fragile identity behind
// instead of staying one inserted row away from pointing somewhere else.
func TestApplyRows_fillsTheIDCellItMatchedPositionally(t *testing.T) {
	path := halfPaired(t)
	// A genuine round trip, which is what `fin edit` performs: read the row, change
	// one field, write it back. Hand-building the transaction hid the answer twice
	// — once by omitting the account, once by omitting the source — and each time
	// the engine was right and the fixture was not what the flow produces.
	changed := editDescription(t, path, "expense-r4", "подкатегорию поправили")

	if err := financexlsx.ApplyRows(path, []domain.Transaction{changed}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	// Column I is where AssignIDs puts the id on this fixture: seven documented
	// columns, the owner's bank in H, the id beside it.
	if v := cellValue(t, path, "Расходы", "I4"); v != "expense-r4" {
		t.Errorf("id cell = %q, want it filled with expense-r4", v)
	}
	// The bank stays where the owner keeps it, rather than being rewritten into
	// Источник or dropped.
	if v := cellValue(t, path, "Расходы", "H4"); v != "Сбербанк" {
		t.Errorf("column H = %q, want the account still beside Источник", v)
	}
	// And the row is still the same row, not a second copy.
	if n := rowCount(t, path); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

// A blank row past the data must not be mistaken for a transaction. The sheet
// carries formatted empty rows below the records, and registering those would
// let an append land on top of one.
func TestApplyRows_blankRowsAreNotPositionalRows(t *testing.T) {
	path := halfPaired(t)
	added := tx(t, "01D", domain.KindExpense,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 33300, "Подписки", "новая")

	if err := financexlsx.ApplyRows(path, []domain.Transaction{added}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	if n := rowCount(t, path); n != 4 {
		t.Fatalf("workbook has %d rows, want 4", n)
	}
	got := readBack(t, path)
	if _, ok := got["01D"]; !ok {
		t.Errorf("the appended row did not come back; workbook holds %v", slices.Sorted(maps.Keys(got)))
	}
	// The row that was identified positionally is still there and still itself.
	if _, ok := got["expense-r4"]; !ok {
		t.Errorf("expense-r4 went missing; workbook holds %v", slices.Sorted(maps.Keys(got)))
	}
}
