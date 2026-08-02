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

// writeBalance puts the amount and the date into the account's row.
//
// No styles are restored afterwards, and that is measured rather than assumed:
// on a cell that already carries a format, excelize leaves the style untouched
// when the value changes, and every cell on this sheet in the live workbook has
// the owner's currency or date format on it. The one case where a write does
// impose a format — a date into a cell that had no style at all — is exactly the
// case rowStyles/applyStyles cannot restore either, since they skip style 0. So
// the restore pass here would have been code that runs and changes nothing.
func writeBalance(f *excelize.File, row int, balance domain.Money, updated time.Time) error {
	values := map[int]any{
		balanceColumn: float64(balance.Kopecks()) / 100,
		updatedColumn: updated,
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
	return nil
}
