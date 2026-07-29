package financexlsx_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// Источник has been carrying two different things: which account the money
// moved through (456 of 507 rows in the live ledger) and how the record was
// captured — "Чек", "Вручную" (50 rows). The Счета sheet is what tells them
// apart, so the vocabulary comes from the workbook itself rather than a list
// baked into the code.
func TestRead_splitsAccountFromSource(t *testing.T) {
	path := workbookWithExtraColumn(t)
	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	byRow := map[string]domain.Transaction{}
	for _, tx := range led.Transactions {
		byRow[tx.ID()] = tx
	}

	// Row 3 has an empty Источник: neither an account nor a capture method.
	if got := byRow["expense-r3"]; got.Account() != "" || got.Source() != "" {
		t.Errorf("row 3: account=%q source=%q, want both empty", got.Account(), got.Source())
	}
	// Row 4 is the live shape: Источник says Чек, the bank sits in the column
	// beside it.
	row4 := byRow["expense-r4"]
	if row4.Source() != "Чек" {
		t.Errorf("row 4 source = %q, want Чек", row4.Source())
	}
	if row4.Account() != "Сбербанк" {
		t.Errorf("row 4 account = %q, want Сбербанк from the column beside Источник", row4.Account())
	}
	// Income names a payer, not an account.
	if got := byRow["income-r3"]; got.Source() != "Зарплата" || got.Account() != "" {
		t.Errorf("income: source=%q account=%q, want Зарплата / empty", got.Source(), got.Account())
	}
}

// When Источник names one of the accounts, that is the account and there is no
// capture method to record.
func TestRead_accountNamedDirectlyInTheSourceColumn(t *testing.T) {
	path := workbookWithExtraColumn(t)
	setCell(t, path, "Расходы", "G3", "Альфа-Банк")

	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, tx := range led.Transactions {
		if tx.ID() != "expense-r3" {
			continue
		}
		if tx.Account() != "Альфа-Банк" {
			t.Errorf("account = %q, want Альфа-Банк", tx.Account())
		}
		if tx.Source() != "" {
			t.Errorf("source = %q, want empty — the column named an account", tx.Source())
		}
	}
}

// The column beside Источник is unlabelled and belongs to the owner. Only a
// value the Счета sheet recognizes is read out of it; anything else is left
// alone rather than guessed at.
func TestRead_ignoresUnrecognizedValuesBesideSource(t *testing.T) {
	path := workbookWithExtraColumn(t)
	setCell(t, path, "Расходы", "H3", "какая-то заметка")

	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, tx := range led.Transactions {
		if tx.ID() == "expense-r3" && tx.Account() != "" {
			t.Errorf("account = %q, want empty — the value is not one of the accounts", tx.Account())
		}
	}
}

// A workbook with no Счета sheet has no vocabulary, so nothing is an account
// and Источник keeps its old meaning. That is what every older workbook looks
// like.
func TestRead_withoutAnAccountsSheet(t *testing.T) {
	path := workbookWithoutAccounts(t)
	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, tx := range led.Transactions {
		if tx.Account() != "" {
			t.Errorf("account = %q, want empty without a Счета sheet", tx.Account())
		}
		if tx.ID() == "expense-r4" && tx.Source() != "Чек" {
			t.Errorf("source = %q, want Чек", tx.Source())
		}
	}
}

func setCell(t *testing.T, path, sheet, cell, value string) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	if err := f.SetCellValue(sheet, cell, value); err != nil {
		t.Fatalf("set %s!%s: %v", sheet, cell, err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = f.Close()
}

// workbookWithoutAccounts is the shape of an older ledger: no Счета sheet, so
// nothing establishes what an account is.
func workbookWithoutAccounts(t *testing.T) string {
	t.Helper()
	path := workbookWithExtraColumn(t)
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	if err := f.DeleteSheet("Счета"); err != nil {
		t.Fatalf("delete Счета: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = f.Close()
	return path
}
