package financexlsx_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

func txWith(t *testing.T, id string, account, source string) domain.Transaction {
	t.Helper()
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:       id,
		Kind:     domain.KindExpense,
		Date:     time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:   domain.NewMoney(50000),
		Category: "Еда",
		Account:  account,
		Source:   source,
		Now:      writeClock,
	})
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

// An account with no capture method goes back where the sheet keeps it: the
// Источник column.
func TestApplyRows_writesTheAccountIntoSource(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path,
		[]domain.Transaction{txWith(t, "01A", "Альфа-Банк", "")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if got := cellValue(t, path, "Расходы", "G3"); got != "Альфа-Банк" {
		t.Errorf("Расходы!G3 = %q, want Альфа-Банк", got)
	}
}

// When the row records how it was captured, Источник keeps that and the account
// goes to the column beside it — the arrangement the live ledger already uses.
func TestApplyRows_writesTheAccountBesideSourceWhenBothArePresent(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path,
		[]domain.Transaction{txWith(t, "01A", "Т-Банк", "Чек")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if got := cellValue(t, path, "Расходы", "G3"); got != "Чек" {
		t.Errorf("Расходы!G3 = %q, want Чек", got)
	}
	if got := cellValue(t, path, "Расходы", "H3"); got != "Т-Банк" {
		t.Errorf("Расходы!H3 = %q, want Т-Банк", got)
	}
}

// Writing a row back exactly as it was read must not move anything. This is the
// property the whole sync rests on: a no-op write has to be a no-op on disk.
func TestApplyRows_roundTripsWithoutMovingColumns(t *testing.T) {
	path := paired(t)
	before := map[string]string{}
	for _, cell := range []string{"G3", "H3", "G4", "H4"} {
		before[cell] = cellValue(t, path, "Расходы", cell)
	}

	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := financexlsx.ApplyRows(path, led.Transactions, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	for cell, want := range before {
		if got := cellValue(t, path, "Расходы", cell); got != want {
			t.Errorf("%s = %q after a round trip, want %q", cell, got, want)
		}
	}
}

// Clearing a row takes the account with it, wherever it was kept.
func TestApplyRows_removalClearsTheAccountBesideSource(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path, nil, []string{"01B"}, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if got := cellValue(t, path, "Расходы", "H4"); got != "" {
		t.Errorf("Расходы!H4 = %q, want the account cleared with the row", got)
	}
}

// Removing the account from a row has to remove it from the sheet. clearRow
// already knew this; writeRow did not, and a write path that only ever adds is
// how a deletion silently comes back: Fingerprint covers Account, so the next
// sync sees the workbook disagreeing with the baseline and pulls the old value
// into the ledger. 19 rows in the live book have exactly this shape.
func TestApplyRows_clearingTheAccountClearsTheColumnBesideSource(t *testing.T) {
	path := paired(t)
	// Row 4 arrives as Источник="Чек", H="Сбербанк" — the live arrangement.
	if got := cellValue(t, path, "Расходы", "H4"); got != "Сбербанк" {
		t.Fatalf("setup: Расходы!H4 = %q, want Сбербанк", got)
	}
	cleared := txWith(t, "01B", "", "Чек")

	if err := financexlsx.ApplyRows(path, []domain.Transaction{cleared}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if got := cellValue(t, path, "Расходы", "H4"); got != "" {
		t.Errorf("Расходы!H4 = %q after the account was cleared through the ledger, want empty", got)
	}
}

// Whatever else is in that column is the owner's, not ours: clearing an account
// must not reach into a note that was never an account name.
func TestApplyRows_clearingTheAccountLeavesAForeignNoteAlone(t *testing.T) {
	path := paired(t)
	setCell(t, path, "Расходы", "H4", "по карте жены")

	if err := financexlsx.ApplyRows(path,
		[]domain.Transaction{txWith(t, "01B", "", "Чек")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if got := cellValue(t, path, "Расходы", "H4"); got != "по карте жены" {
		t.Errorf("Расходы!H4 = %q, want the owner's note untouched", got)
	}
}

// The id column and the account column are chosen by two different rules, and
// on a workbook whose eighth column is empty both land on 8. Then the account
// is written and the id overwrites it in the same pass.
//
// This is invisible on the owner's file, where 19 filled cells push the id to
// column I. Any other book — a fresh one, or this one before those rows existed
// — hits it on the first --init.
func TestApplyRows_accountSurvivesWhenTheIDColumnWouldCollide(t *testing.T) {
	path := workbookWithoutExtraColumn(t)
	if err := financexlsx.AssignIDs(path, map[string]string{
		"expense-r3": "01A", "expense-r4": "01B", "income-r3": "01C",
	}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}

	if err := financexlsx.ApplyRows(path,
		[]domain.Transaction{txWith(t, "01A", "Сбербанк", "Чек")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var found bool
	for _, got := range led.Transactions {
		if got.ID() != "01A" {
			continue
		}
		found = true
		if got.Account() != "Сбербанк" {
			t.Errorf("account = %q, want Сбербанк — the id column overwrote it", got.Account())
		}
		if got.Source() != "Чек" {
			t.Errorf("source = %q, want Чек", got.Source())
		}
	}
	if !found {
		t.Fatal("row 01A not read back")
	}
}
