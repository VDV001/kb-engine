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
	if plan := finance.Diff(ledger, workbook, baseline); plan.Direction != finance.DirectionToLedger {
		t.Fatalf("Direction = %v, want ToLedger", plan.Direction)
	}

	got, err := finance.ApplyToLedger(ledger, workbook, syncedAt)
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

	editedB := expenseTx(t, "01B", 30, 77700, "Транспорт", "")
	ledger := []finance.Record{recordOf(t, a, 5), recordOf(t, b, 2)}
	workbook := []domain.Transaction{a, editedB}

	got, err := finance.ApplyToLedger(ledger, workbook, syncedAt)
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

	got, err := finance.ApplyToLedger(ledger, workbook, syncedAt)
	if err != nil {
		t.Fatalf("ApplyToLedger: %v", err)
	}
	if len(got) != 2 || got[0].Transaction().ID() != "01B" {
		t.Errorf("records are not in date order: %v", got)
	}
}

// Applying makes one side match the other, so it is measured against that
// side and not against the baseline.
//
// The first version took the plan and read its Modified list to decide what to
// rewrite. That works while the direction follows the diff and breaks the
// moment --resolve overrides it: resolving to the ledger would leave an edit
// the workbook made in place, because the baseline said the ledger had not
// touched that row. Comparing the two sides directly has no such gap, and the
// conflict decision moves to the caller, which is where it belongs.
func TestToWorkbook_makesTheWorkbookMatchTheLedger(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	b := expenseTx(t, "01B", 30, 50000, "Транспорт", "")
	onlyInLedger := expenseTx(t, "01C", 31, 10000, "Подписки", "из терминала")

	// The workbook: 01A edited behind the ledger's back, 01B untouched, 01D is
	// a row the ledger does not have.
	editedA := expenseTx(t, "01A", 29, 99900, "Еда", "переписано")
	onlyInWorkbook := expenseTx(t, "01D", 28, 40000, "Еда", "")

	upserts, removals := finance.ToWorkbook(
		[]finance.Record{recordOf(t, a, 1), recordOf(t, b, 1), recordOf(t, onlyInLedger, 1)},
		[]domain.Transaction{editedA, b, onlyInWorkbook},
	)

	got := map[string]bool{}
	for _, tx := range upserts {
		got[tx.ID()] = true
	}
	if !got["01A"] {
		t.Error("a row the workbook edited must be written back from the ledger")
	}
	if !got["01C"] {
		t.Error("a row only the ledger has must be added to the workbook")
	}
	if got["01B"] {
		t.Error("an identical row must not be rewritten")
	}
	if len(removals) != 1 || removals[0] != "01D" {
		t.Errorf("removals = %v, want [01D] — the workbook holds a row the ledger does not", removals)
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
