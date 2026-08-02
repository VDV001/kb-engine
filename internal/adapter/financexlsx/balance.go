package financexlsx

import (
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// balanceColumns are the 1-based columns one account occupies on the Счета
// sheet: Банк | Баланс | Обновлено. The reader takes them in the same order,
// and the two would have to agree even if they were written apart.
const (
	bankColumn    = 1
	balanceColumn = 2
	updatedColumn = 3
)

// SetBalance records a new balance for one account on the Счета sheet.
//
// This is the sheet the engine has only ever read. Until now the balance was
// updated by writing straight into the cells from outside, which meant the
// workbook had two writers — and two writers of one file eventually disagree
// about what it says.
//
// The account is found by name. Addressing it by row number is what the outside
// writer did, and a single inserted row would have moved every balance one bank
// over without anything looking wrong.
func SetBalance(path, bank string, balance domain.Money, now func() time.Time) error {
	if err := CheckLock(path); err != nil {
		return err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	row, err := accountRow(f, bank)
	if err != nil {
		return err
	}

	// The domain decides whether this is a valid snapshot — a blank bank, a date
	// in the future. Checking it here as well would put the same rule in two
	// places, and the copy in the adapter is the one that drifts.
	if _, err := domain.NewAccount(bank, balance, now(), now); err != nil {
		return err
	}

	if err := backup(path, now); err != nil {
		return err
	}
	if err := writeBalance(f, row, balance, now()); err != nil {
		return err
	}
	return saveAtomically(f, path)
}

// accountRow finds the row for a bank on the Счета sheet, or refuses and names
// the banks that are there.
//
// A name the sheet does not list is a question rather than a row to add: that
// sheet is the vocabulary deciding what counts as an account everywhere else in
// the book, so writing into it invents a word the rest of the book will then
// read back.
func accountRow(f *excelize.File, bank string) (int, error) {
	rows, err := f.GetRows(sheetAccounts, excelize.Options{RawCellValue: true})
	if err != nil {
		return 0, fmt.Errorf("%w: the workbook has no %s sheet", ErrUnknownAccount, sheetAccounts)
	}

	want := strings.TrimSpace(bank)
	var known []string
	for i, r := range rows {
		rowNum := i + 1
		if rowNum < firstDataRow {
			continue
		}
		name := strings.TrimSpace(cell(r, bankColumn-1))
		if name == "" {
			continue
		}
		if name == want {
			return rowNum, nil
		}
		known = append(known, name)
	}
	return 0, fmt.Errorf("%w: %q — the %s sheet lists %s",
		ErrUnknownAccount, bank, sheetAccounts, strings.Join(known, ", "))
}

// writeBalance puts the amount and the date into the account's row, keeping the
// formatting those cells already carry.
//
// Both halves of this were learned on the owner's book rather than on a fixture,
// and neither showed up in tests written before it was tried:
//
// The date is stored as a whole day. A spreadsheet keeps dates as days since
// 1900, so writing the moment leaves 46236.69 in the cell — a balance is
// confirmed on a day, and the fraction is the clock leaking into the record.
//
// The styles are put back because on the live workbook a date write replaces
// them: measured 104 → 53, and 53 carries no format at all, so the cell stopped
// reading as a date and started reading as 46236.69. A fixture built by excelize
// does not reproduce that — it keeps its own custom formats — which is why the
// first version of this function skipped the restore and the tests agreed.
func writeBalance(f *excelize.File, row int, balance domain.Money, updated time.Time) error {
	styles, err := rowStyles(f, sheetAccounts, row, []int{balanceColumn, updatedColumn})
	if err != nil {
		return err
	}

	y, m, d := updated.Date()
	values := map[int]any{
		balanceColumn: float64(balance.Kopecks()) / 100,
		updatedColumn: time.Date(y, m, d, 0, 0, 0, 0, updated.Location()),
	}
	for col, v := range values {
		name, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return fmt.Errorf("%s row %d: %w", sheetAccounts, row, err)
		}
		if err := f.SetCellValue(sheetAccounts, name, v); err != nil {
			return fmt.Errorf("%s!%s: %w", sheetAccounts, name, err)
		}
	}

	// Styles last: writing a time.Time applies a format of its own, and the
	// sheet's own formatting has to win over it.
	return applyStyles(f, sheetAccounts, row, styles)
}
