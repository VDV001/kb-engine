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

// balanceColumns are the 1-based columns one account occupies on the Счета
// sheet: Банк | Баланс | Обновлено. The reader takes them in the same order,
// and the two would have to agree even if they were written apart.
const (
	bankColumn     = 1
	balanceColumn  = 2
	updatedColumn  = 3
	currencyColumn = 4
	rateColumn     = 5
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
	return saveAndRemember(f, path, bank, now)
}

// ErrAccountExists is returned when a new account is asked for under a name the
// sheet already holds — including another spelling of it.
var ErrAccountExists = errors.New("the workbook already knows this account")

// AddAccount appends a new account to the Счета sheet.
//
// Creating a row and updating a balance are different intentions, so they are
// different functions rather than one with a flag. SetBalance keeps refusing a
// name it does not know, and that refusal is the point: a typo in --bank would
// otherwise put a word into the vocabulary the rest of the book reads back.
// This is the second door, the one a person walks through saying «I know it is
// not there».
//
// Everything else about the row is deliberately ordinary. Once it exists, its
// balance is confirmed by SetBalance like any other account's — a debt that
// needed its own command to be updated would be a second way to write money
// into the book, and the book has one writer.
func AddAccount(path, bank string, balance domain.Money, currency domain.Currency, rate domain.Rate, now func() time.Time) error {
	if err := CheckLock(path); err != nil {
		return err
	}

	// The domain owns what a valid account is — a blank name, a date ahead of
	// the clock. Checked before the file is touched: a refusal has to leave the
	// book exactly as it was, and half-done work is worse than none because
	// nobody goes looking for it.
	if _, err := domain.NewForeignAccount(bank, balance, currency, rate, now(), now); err != nil {
		return err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	last, err := lastAccountRow(f, bank)
	if err != nil {
		return err
	}
	row := last + 1

	if err := backup(path, now); err != nil {
		return err
	}
	name, err := excelize.CoordinatesToCellName(bankColumn, row)
	if err != nil {
		return fmt.Errorf("%s row %d: %w", sheetAccounts, row, err)
	}
	if err := f.SetCellValue(sheetAccounts, name, strings.TrimSpace(bank)); err != nil {
		return fmt.Errorf("%s!%s: %w", sheetAccounts, name, err)
	}
	// The new row inherits the formatting of the one above it: on this sheet
	// the format is the whole difference between 3000 and «3 000,00 ₽», and a
	// row that carries none is visible as the one the engine added.
	styles, err := rowStyles(f, sheetAccounts, last, []int{bankColumn, balanceColumn, updatedColumn})
	if err != nil {
		return err
	}
	if err := applyStyles(f, sheetAccounts, row, styles); err != nil {
		return err
	}
	if err := writeBalance(f, row, balance, now()); err != nil {
		return err
	}
	if err := writeCurrency(f, row, currency, rate); err != nil {
		return err
	}
	return saveAndRemember(f, path, bank, now)
}

// lastAccountRow returns the row of the last account on the sheet, refusing
// when the sheet already holds the name under any spelling.
//
// Case, «ё» and hyphens do not distinguish accounts — the domain decides that,
// and the quick-entry vocabulary asks the same question of the same rule. A
// letter-by-letter comparison here would let «сбер банк» in beside «Сбербанк»,
// and both rows would then look equally plausible.
func lastAccountRow(f *excelize.File, bank string) (int, error) {
	rows, err := f.GetRows(sheetAccounts, excelize.Options{RawCellValue: true})
	if err != nil {
		return 0, fmt.Errorf("%w: the workbook has no %s sheet", ErrUnknownAccount, sheetAccounts)
	}

	last := firstDataRow - 1
	for i, r := range rows {
		rowNum := i + 1
		if rowNum < firstDataRow {
			continue
		}
		name := strings.TrimSpace(cell(r, bankColumn-1))
		if name == "" {
			continue
		}
		if domain.SameAccountName(name, bank) {
			return 0, fmt.Errorf("%w: %q is already on the %s sheet as %q",
				ErrAccountExists, strings.TrimSpace(bank), sheetAccounts, name)
		}
		last = rowNum
	}
	return last, nil
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
		// Написание решает домен, как и при заведении счёта строкой ниже. Лист
		// «Счета» и то, что человек набирает в терминале, расходятся регистром
		// и пробелами вокруг стрелки, и побайтовое равенство давало про один
		// счёт два ответа: «уже есть» на --create и «такого нет» здесь.
		if domain.SameAccountName(name, want) {
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

// saveAndRemember сохраняет книгу и запоминает, КОГДА остаток был подтверждён.
//
// Момент нужен расчёту и не нужен человеку: колонка «Обновлено» хранит день,
// потому что её читают глазами, а спор «видел ли человек эту трату» внутри дня
// днём не решается. Оба писателя баланса — команда и экран терминала — проходят
// здесь, поэтому потерять момент, выбрав другую поверхность, нельзя.
//
// Неудача записи момента не отменяет записанный баланс — книга уже сохранена, —
// но и молчать о ней нельзя: без момента расчёт вернётся к приблизительному
// правилу и завысит остаток на траты, записанные после подтверждения.
func saveAndRemember(f *excelize.File, path, bank string, now func() time.Time) error {
	if err := saveAtomically(f, path); err != nil {
		return err
	}
	if err := balancestate.Record(balancestate.PathNextTo(path), bank, now()); err != nil {
		return fmt.Errorf("balance written, confirmation moment not: %w", err)
	}
	return nil
}

// writeCurrency записывает валюту и курс счёта в свои колонки.
//
// Рублёвый счёт колонок не получает вовсе: пустая ячейка и есть «валюта книги»
// по конструкции чтения, а «RUB» с курсом «1» были бы двумя лишними значениями,
// которые однажды разойдутся с этим правилом.
func writeCurrency(f *excelize.File, row int, currency domain.Currency, rate domain.Rate) error {
	if currency.IsBase() {
		return nil
	}
	name, err := excelize.CoordinatesToCellName(currencyColumn, row)
	if err != nil {
		return fmt.Errorf("%s row %d: %w", sheetAccounts, row, err)
	}
	if err := f.SetCellValue(sheetAccounts, name, currency.Code()); err != nil {
		return fmt.Errorf("%s!%s: %w", sheetAccounts, name, err)
	}
	per, ok := rate.PerUnit()
	if !ok {
		return nil // курс неизвестен — ячейка остаётся пустой, а не нулём
	}
	cell, err := excelize.CoordinatesToCellName(rateColumn, row)
	if err != nil {
		return fmt.Errorf("%s row %d: %w", sheetAccounts, row, err)
	}
	if err := f.SetCellValue(sheetAccounts, cell, float64(per.Kopecks())/100); err != nil {
		return fmt.Errorf("%s!%s: %w", sheetAccounts, cell, err)
	}
	return nil
}

// SetCurrency меняет валюту и курс у счёта, который на листе уже есть.
func SetCurrency(path, bank string, currency domain.Currency, rate domain.Rate, now func() time.Time) error {
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
	if err := backup(path, now); err != nil {
		return err
	}
	if err := writeCurrency(f, row, currency, rate); err != nil {
		return err
	}
	return saveAndRemember(f, path, bank, now)
}
