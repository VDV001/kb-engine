package financexlsx

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/balancestate"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// ErrAccountInUse is returned when rows of the Расходы sheet name the account.
//
// The Счета sheet is the vocabulary the reader uses to split that column into an
// account and a source. Renamed there and not in those rows, the old name would
// read back as a source: history would quietly change its meaning and the next
// sync would refuse the rows.
var ErrAccountInUse = errors.New("expense rows name this account")

// RenameAccount changes what an account is called on the Счета sheet.
//
// Род счёта читается из имени («Займ → Коллеге» — род «Займ», имя без стрелки —
// обычный счёт), поэтому переименование — единственный способ перенести счёт из
// рода в род. Баланс, дата подтверждения и валюта остаются: меняется слово, а не
// деньги, и дата не сдвигается, потому что остаток никто заново не смотрел.
//
// Все отказы проверяются до записи: отказ обязан оставить книгу такой, какой она
// была.
func RenameAccount(path, from, to string, now func() time.Time) error {
	if err := CheckLock(path); err != nil {
		return err
	}
	// Что считается допустимым именем, решает домен — тот же вход, что у заведения.
	var zero domain.Money
	if _, err := domain.NewAccount(to, zero, now(), now); err != nil {
		return err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	row, current, err := renameTarget(f, from, to)
	if err != nil {
		return err
	}
	name, err := excelize.CoordinatesToCellName(bankColumn, row)
	if err != nil {
		return fmt.Errorf("%s row %d: %w", sheetAccounts, row, err)
	}

	if err := backup(path, now); err != nil {
		return err
	}
	// SetCellValue keeps the cell's style: the format of the name stays as the
	// owner set it.
	if err := f.SetCellValue(sheetAccounts, name, strings.TrimSpace(to)); err != nil {
		return fmt.Errorf("%s!%s: %w", sheetAccounts, name, err)
	}
	if err := saveAtomically(f, path); err != nil {
		return err
	}
	if err := balancestate.Rename(balancestate.PathNextTo(path), current, strings.TrimSpace(to)); err != nil {
		return fmt.Errorf("account renamed, confirmation moment not moved: %w", err)
	}
	return nil
}

// renameTarget finds the row to rename and checks every refusal that depends on
// the book: the new name taken by another account, and expense rows naming the
// old one. It returns the old name as the sheet spells it.
func renameTarget(f *excelize.File, from, to string) (row int, current string, err error) {
	row, err = accountRow(f, from)
	if err != nil {
		return 0, "", err
	}
	name, err := excelize.CoordinatesToCellName(bankColumn, row)
	if err != nil {
		return 0, "", fmt.Errorf("%s row %d: %w", sheetAccounts, row, err)
	}
	current, err = f.GetCellValue(sheetAccounts, name)
	if err != nil {
		return 0, "", fmt.Errorf("%s!%s: %w", sheetAccounts, name, err)
	}
	current = strings.TrimSpace(current)

	// Новое имя, которое лист уже знает, — это слияние двух счетов, а не
	// переименование. Своё же имя в другом написании допустимо: так правят
	// регистр и пробелы вокруг стрелки.
	if !domain.SameAccountName(current, to) {
		if taken, err := accountRow(f, to); err == nil && taken != row {
			return 0, "", fmt.Errorf("%w: %q", ErrAccountExists, to)
		}
	}
	n, err := expenseRowsNaming(f, current)
	if err != nil {
		return 0, "", err
	}
	if n > 0 {
		return 0, "", fmt.Errorf("%w: %q is named by %d row(s) of %s — a renamed account would leave them reading it as a source",
			ErrAccountInUse, current, n, sheetExpenses)
	}
	return row, current, nil
}

// expenseRowsNaming counts Расходы rows that name the account, in either of
// the two columns the reader takes an account from (see splitAccount).
func expenseRowsNaming(f *excelize.File, bank string) (int, error) {
	rows, err := f.GetRows(sheetExpenses, excelize.Options{RawCellValue: true})
	if err != nil {
		return 0, fmt.Errorf("read sheet %q: %w", sheetExpenses, err)
	}
	beside := len(dataColumns(domain.KindExpense))
	n := 0
	for i, r := range rows {
		if i+1 < firstDataRow {
			continue
		}
		for _, col := range []int{6, beside} {
			if v := strings.TrimSpace(cell(r, col)); v != "" && domain.SameAccountName(v, bank) {
				n++
				break
			}
		}
	}
	return n, nil
}
