package financexlsx_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// The kind decides which sheet a row lives on, so correcting the kind of an
// existing row means moving it. The write placed the corrected row on its new
// sheet and left the original where it was: the workbook then held the same id
// twice, once as an expense and once as an income, and the net it shows was
// wrong by both amounts.
//
// The duplicate is caught on the next sync — Read finds two rows with one id and
// stops — so this is louder than the other findings. It is still a silent loss
// while it lasts: the file the owner opens and reads by eye is wrong, and the
// command that made it wrong reported "1 row(s) written".
//
// A removal is not enough to describe this. The ledger still holds the id, so
// nothing asks for its removal; only the writer knows the row changed sheets.
func TestApplyRows_movesARowWhoseKindChanged(t *testing.T) {
	path := paired(t)

	corrected, err := domain.NewTransaction(domain.TransactionParams{
		ID:     "01A", // an expense on Расходы in the fixture
		Kind:   domain.KindIncome,
		Date:   time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount: domain.NewMoney(50000),
		Source: "Возврат",
		Now:    writeClock,
	})
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}

	if err := financexlsx.ApplyRows(path, []domain.Transaction{corrected}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	// Counted over the list, not the map readBack builds: two rows sharing an id
	// collapse into one map entry, which is exactly how this stayed invisible.
	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	seen := 0
	for _, tx := range led.Transactions {
		if tx.ID() == "01A" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the workbook holds %d rows with id 01A, want 1", seen)
	}

	got := readBack(t, path)
	tx, ok := got["01A"]
	if !ok {
		t.Fatal("row 01A is gone after correcting its kind")
	}
	if tx.IsExpense() {
		t.Error("row 01A is still an expense — the corrected row did not win")
	}
	if got := tx.Amount().Kopecks(); got != 50000 {
		t.Errorf("row 01A amount = %d kopecks, want 50000", got)
	}

	// The vacated row is empty, not merely unreferenced: an amount left behind
	// still sums into whatever the owner selects in the spreadsheet.
	for _, cell := range []string{"D3", "I3"} {
		if v := cellValue(t, path, "Расходы", cell); v != "" {
			t.Errorf("Расходы!%s = %q after the row moved, want empty", cell, v)
		}
	}

	// And the rows that had nothing to do with it are untouched.
	for _, id := range []string{"01B", "01C"} {
		if _, ok := got[id]; !ok {
			t.Errorf("row %s disappeared while another row moved", id)
		}
	}
}
