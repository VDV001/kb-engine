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
