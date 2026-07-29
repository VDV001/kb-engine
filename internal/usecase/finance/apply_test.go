package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

var syncedAt = time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)

func TestApplyToLedger(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	b := expenseTx(t, "01B", 30, 50000, "Транспорт", "")
	c := expenseTx(t, "01C", 31, 10000, "Подписки", "")
	baseline := stateOf(t, a, b)

	// The workbook: 01A edited, 01B gone, 01C new.
	editedA := expenseTx(t, "01A", 29, 99900, "Еда", "переписано")
	workbook := []domain.Transaction{editedA, c}

	ledger := []finance.Record{recordOf(t, a, 3), recordOf(t, b, 1)}
	plan := finance.Diff(ledger, workbook, baseline)
	if plan.Direction != finance.DirectionToLedger {
		t.Fatalf("Direction = %v, want ToLedger", plan.Direction)
	}

	got, err := finance.ApplyToLedger(ledger, workbook, plan, syncedAt)
	if err != nil {
		t.Fatalf("ApplyToLedger: %v", err)
	}
	byID := map[string]finance.Record{}
	for _, r := range got {
		byID[r.Transaction().ID()] = r
	}

	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if _, still := byID["01B"]; still {
		t.Error("a row removed from the workbook must leave the ledger")
	}

	// An edited row keeps its identity and advances one revision. Rev is what a
	// later sync reads to tell "this changed" from "this was always so".
	edited := byID["01A"]
	if edited.Rev() != 4 {
		t.Errorf("rev = %d, want 4 (was 3)", edited.Rev())
	}
	if !edited.UpdatedAt().Equal(syncedAt) {
		t.Errorf("updatedAt = %s, want the sync clock", edited.UpdatedAt())
	}
	if edited.Transaction().Amount().Kopecks() != 99900 || edited.Transaction().Description() != "переписано" {
		t.Errorf("edited row did not take the workbook's content: %s / %q",
			edited.Transaction().Amount(), edited.Transaction().Description())
	}

	if added := byID["01C"]; added.Rev() != 1 {
		t.Errorf("a row the ledger has never seen arrives at rev %d, want 1", added.Rev())
	}
}

// A row neither side touched keeps its revision and its timestamp. Bumping
// everything on every sync would make rev meaningless and rewrite the whole
// file for one edit.
func TestApplyToLedger_leavesUntouchedRowsAlone(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	b := expenseTx(t, "01B", 30, 50000, "Транспорт", "")
	baseline := stateOf(t, a, b)

	editedB := expenseTx(t, "01B", 30, 77700, "Транспорт", "")
	ledger := []finance.Record{recordOf(t, a, 5), recordOf(t, b, 2)}
	workbook := []domain.Transaction{a, editedB}

	plan := finance.Diff(ledger, workbook, baseline)
	got, err := finance.ApplyToLedger(ledger, workbook, plan, syncedAt)
	if err != nil {
		t.Fatalf("ApplyToLedger: %v", err)
	}
	for _, r := range got {
		if r.Transaction().ID() != "01A" {
			continue
		}
		if r.Rev() != 5 {
			t.Errorf("untouched row rev = %d, want 5", r.Rev())
		}
		if !r.UpdatedAt().Equal(importedAt) {
			t.Errorf("untouched row updatedAt = %s, want it unchanged", r.UpdatedAt())
		}
	}
}

// The ledger stays chronological whichever way the sync ran, even when the
// workbook hands the rows over in sheet order.
func TestApplyToLedger_keepsTheFileSorted(t *testing.T) {
	a := expenseTx(t, "01A", 30, 100, "Еда", "")
	b := expenseTx(t, "01B", 29, 100, "Еда", "")
	ledger := []finance.Record{recordOf(t, a, 1), recordOf(t, b, 1)}
	workbook := []domain.Transaction{a, b}

	plan := finance.Diff(ledger, workbook, stateOf(t, a, b))
	if plan.Direction != finance.DirectionNone {
		t.Fatalf("Direction = %v, want None", plan.Direction)
	}
	got, err := finance.ApplyToLedger(ledger, workbook, plan, syncedAt)
	if err != nil {
		t.Fatalf("ApplyToLedger: %v", err)
	}
	if len(got) != 2 || got[0].Transaction().ID() != "01B" {
		t.Errorf("records are not in date order: %v", got)
	}
}

// Applying is only ever a consequence of a decision already made. Being handed
// a conflict means the caller skipped the decision.
func TestApplyToLedger_refusesAConflict(t *testing.T) {
	_, err := finance.ApplyToLedger(nil, nil, finance.Plan{Direction: finance.DirectionConflict}, syncedAt)
	if err == nil {
		t.Error("expected a refusal when handed a conflicting plan")
	}
}

// Baseline is what the state file records after a sync: the content of every
// row as both sides now agree it stands.
func TestBaselineOf(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	b := expenseTx(t, "01B", 30, 50000, "Транспорт", "")
	st := finance.BaselineOf([]finance.Record{recordOf(t, a, 1), recordOf(t, b, 1)}, syncedAt)

	if !st.SyncedAt.Equal(syncedAt) {
		t.Errorf("SyncedAt = %s, want the sync clock", st.SyncedAt)
	}
	if len(st.Rows) != 2 {
		t.Fatalf("Rows = %v, want two entries", st.Rows)
	}
	if st.Rows["01A"] != finance.Fingerprint(a) {
		t.Error("baseline does not carry the row's fingerprint")
	}
}
