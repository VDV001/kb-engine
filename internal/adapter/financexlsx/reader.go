// Package financexlsx reads the hand-kept finance workbook into the domain.
// It is the anti-corruption layer for the ledger: blank rows, mixed date
// encodings and amounts written with commas or grouping spaces are normalized
// here so the domain stays strict.
package financexlsx

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// Sheet and column layout of Учёт_финансов.xlsx. Data starts at row 3: row 1 is
// a title, row 2 the header.
const (
	sheetExpenses = "Расходы"
	sheetIncome   = "Доходы"
	sheetAccounts = "Счета"
	firstDataRow  = 3
)

// Ledger is everything the workbook holds, already validated.
type Ledger struct {
	Transactions []domain.Transaction
	Accounts     []domain.Account
}

// TotalBalance sums the balances across accounts.
func (l Ledger) TotalBalance() domain.Money {
	var total domain.Money
	for _, a := range l.Accounts {
		total = total.Add(a.Balance())
	}
	return total
}

// Net sums signed transaction amounts: income minus expenses.
func (l Ledger) Net() domain.Money {
	var total domain.Money
	for _, t := range l.Transactions {
		total = total.Add(t.SignedAmount())
	}
	return total
}

// Read loads the workbook at path. The clock is passed through to the domain so
// validation does not depend on ambient time.
func Read(path string, now func() time.Time) (Ledger, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return Ledger{}, fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	var led Ledger

	// Accounts first: their names are the vocabulary that decides whether
	// Источник is naming an account or a way the record was captured.
	if led.Accounts, err = readAccounts(f, now); err != nil {
		return Ledger{}, err
	}
	known := make(map[string]struct{}, len(led.Accounts))
	for _, a := range led.Accounts {
		known[a.Bank()] = struct{}{}
	}

	expenses, err := readTransactions(f, sheetExpenses, domain.KindExpense, known, now)
	if err != nil {
		return Ledger{}, err
	}
	income, err := readTransactions(f, sheetIncome, domain.KindIncome, known, now)
	if err != nil {
		return Ledger{}, err
	}
	led.Transactions = append(expenses, income...)
	return led, nil
}

// column indexes differ per sheet; expenses carry the richer layout.
func readTransactions(f *excelize.File, sheet, kind string, accounts map[string]struct{}, now func() time.Time) ([]domain.Transaction, error) {
	// RawCellValue: the workbook applies a display format (thousands separator,
	// currency suffix, locale-dependent dates), and reading formatted values
	// hands that presentation to the parser. Raw values are the numbers Excel
	// actually stores — "1600" and a date serial — which is what the domain wants.
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
	}

	// Once the workbook carries ids, they are the identity. A sheet that has
	// never been synced has no id column, and the positional fallback below
	// stands in until AssignIDs retires it.
	idCol := findIDColumn(rows)

	var out []domain.Transaction
	for i, row := range rows {
		rowNum := i + 1
		if rowNum < firstDataRow {
			continue
		}

		p := domain.TransactionParams{Kind: kind, Now: now}
		var rawAmount string
		if kind == domain.KindExpense {
			// Дата | Категория | Подкатегория | Место | Описание | Сумма | Источник
			p.Category, p.Subcategory = cell(row, 1), cell(row, 2)
			p.Place, p.Description = cell(row, 3), cell(row, 4)
			rawAmount = cell(row, 5)
			p.Account, p.Source = splitAccount(row, accounts)
		} else {
			// Дата | Источник | Описание | Сумма
			p.Source, p.Description = cell(row, 1), cell(row, 2)
			rawAmount = cell(row, 3)
		}

		// A blank row mid-sheet is normal in a hand-kept ledger — skip it rather
		// than importing a zero transaction.
		if rawAmount == "" && p.Category == "" && p.Source == "" {
			continue
		}

		if p.Amount, err = parseAmount(rawAmount); err != nil {
			return nil, fmt.Errorf("%s row %d: amount: %w", sheet, rowNum, err)
		}
		if p.Date, err = parseDate(cell(row, 0)); err != nil {
			return nil, fmt.Errorf("%s row %d: date: %w", sheet, rowNum, err)
		}

		p.ID = rowID(row, idCol, kind, rowNum)

		tx, err := domain.NewTransaction(p)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", sheet, rowNum, err)
		}
		out = append(out, tx)
	}
	return out, nil
}

// splitAccount separates the two things the Источник column has been holding:
// which account the money moved through, and how the record was captured.
//
// A value the Счета sheet names is the account. Anything else — "Чек",
// "Вручную" — stays the source, and for those rows the account may be in the
// unlabelled column immediately after the documented ones, which is where the
// live ledger keeps it on 19 rows.
//
// That trailing column is only read when it holds a name the Счета sheet
// recognizes. It is the owner's column, and guessing at whatever else might sit
// there is exactly the mistake the id column was placed to avoid.
func splitAccount(row []string, accounts map[string]struct{}) (account, source string) {
	source = cell(row, 6)
	if _, ok := accounts[source]; ok {
		return source, ""
	}
	beside := cell(row, len(dataColumns(domain.KindExpense)))
	if _, ok := accounts[beside]; ok {
		return beside, source
	}
	return "", source
}

// rowID returns the identity of a row: the stored id when the workbook has an
// id column, and a positional one otherwise.
//
// Positional identity is the fallback, not the plan: inserting a row above
// shifts every id below it, which is stable enough to read and report but not
// to diff two sides of a sync. AssignIDs replaces it with a stored ULID, and
// from then on that column wins.
func rowID(row []string, idCol int, kind string, rowNum int) string {
	if idCol != 0 {
		if stored := cell(row, idCol-1); stored != "" {
			return stored
		}
	}
	return fmt.Sprintf("%s-r%d", kind, rowNum)
}

func readAccounts(f *excelize.File, now func() time.Time) ([]domain.Account, error) {
	rows, err := f.GetRows(sheetAccounts, excelize.Options{RawCellValue: true})
	if err != nil {
		// The sheet is optional: an older workbook may not have it yet.
		return nil, nil //nolint:nilerr // absence of the sheet is not a failure
	}

	var out []domain.Account
	for i, row := range rows {
		rowNum := i + 1
		if rowNum < firstDataRow {
			continue
		}
		bank, rawBalance := cell(row, 0), cell(row, 1)
		if bank == "" && rawBalance == "" {
			continue
		}
		balance, err := parseAmount(rawBalance)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: balance: %w", sheetAccounts, rowNum, err)
		}
		updated, err := parseDate(cell(row, 2))
		if err != nil {
			return nil, fmt.Errorf("%s row %d: updated: %w", sheetAccounts, rowNum, err)
		}
		acc, err := domain.NewAccount(bank, balance, updated, now)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", sheetAccounts, rowNum, err)
		}
		out = append(out, acc)
	}
	return out, nil
}

// parseAmount reads a raw cell into exact kopecks.
//
// A numeric cell is a float in storage — 89.99 comes back as
// "89.98999999999999" — so it goes through MoneyFromFloat and rounds to the
// kopeck. Anything non-numeric was typed as text and goes through the strict
// parser, which still rejects more precision than a kopeck.
func parseAmount(raw string) (domain.Money, error) {
	if f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
		return domain.MoneyFromFloat(f), nil
	}
	return domain.ParseMoney(raw)
}

// cell returns a trimmed value, or "" when the row is shorter than the index.
func cell(row []string, i int) string {
	if i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// parseDate accepts what raw cells contain: an Excel date serial (the usual
// case — 46110 is 2026-03-29) or a text date typed by hand.
func parseDate(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	if serial, err := strconv.ParseFloat(s, 64); err == nil {
		t, err := excelize.ExcelDateToTime(serial, false)
		if err != nil {
			return time.Time{}, fmt.Errorf("date serial %s: %w", s, err)
		}
		return t, nil
	}
	for _, layout := range []string{time.DateOnly, "2006-01-02 15:04:05", "02.01.2006", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q", raw)
}
