package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

func expenseTx(t *testing.T, id string, day int, kopecks int64, category, note string) domain.Transaction {
	t.Helper()
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:          id,
		Kind:        domain.KindExpense,
		Date:        time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(kopecks),
		Category:    category,
		Description: note,
		Now:         clock,
	})
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

func expenseWithAccount(t *testing.T, id string, day int, amount int64, category, note, account string) domain.Transaction {
	t.Helper()
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:          id,
		Kind:        domain.KindExpense,
		Date:        time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(amount),
		Category:    category,
		Description: note,
		Account:     account,
		Now:         clock,
	})
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

func recordOf(t *testing.T, tx domain.Transaction, rev int) finance.Record {
	t.Helper()
	r, err := finance.NewRecord(tx, rev, importedAt)
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}
	return r
}

// The fingerprint answers one question: has this row's content changed. The id
// is deliberately not part of it — identity is what the fingerprint is looked
// up by, so folding it in would make every comparison trivially true.
func TestFingerprint(t *testing.T) {
	base := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")

	same := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	if finance.Fingerprint(base) != finance.Fingerprint(same) {
		t.Error("identical content must fingerprint identically")
	}

	renamed := expenseTx(t, "01Z", 29, 20245, "Еда", "хлеб")
	if finance.Fingerprint(base) != finance.Fingerprint(renamed) {
		t.Error("the id must not take part in the fingerprint")
	}

	for _, tt := range []struct {
		name string
		tx   domain.Transaction
	}{
		{"amount", expenseTx(t, "01A", 29, 20246, "Еда", "хлеб")},
		{"date", expenseTx(t, "01A", 30, 20245, "Еда", "хлеб")},
		{"category", expenseTx(t, "01A", 29, 20245, "Транспорт", "хлеб")},
		{"description", expenseTx(t, "01A", 29, 20245, "Еда", "молоко")},
		// Moving the same spend from one card to another is a real change, and a
		// sync that cannot see it would leave the two sides silently disagreeing.
		{"account", expenseWithAccount(t, "01A", 29, 20245, "Еда", "хлеб", "Альфа-Банк")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if finance.Fingerprint(base) == finance.Fingerprint(tt.tx) {
				t.Errorf("a changed %s must change the fingerprint", tt.name)
			}
		})
	}
}

// stateOf builds the baseline both sides are compared against.
func stateOf(t *testing.T, txs ...domain.Transaction) finance.SyncState {
	t.Helper()
	rows := make(map[string]string, len(txs))
	for _, tx := range txs {
		rows[tx.ID()] = finance.Fingerprint(tx)
	}
	return finance.SyncState{SyncedAt: importedAt, Rows: rows}
}

func TestDiff_directions(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	b := expenseTx(t, "01B", 30, 50000, "Транспорт", "")
	baseline := stateOf(t, a, b)

	changedA := expenseTx(t, "01A", 29, 99900, "Еда", "хлеб")
	added := expenseTx(t, "01C", 31, 10000, "Еда", "новое")

	tests := []struct {
		name     string
		ledger   []domain.Transaction
		workbook []domain.Transaction
		want     finance.Direction
	}{
		{"neither side moved", []domain.Transaction{a, b}, []domain.Transaction{a, b}, finance.DirectionNone},
		{"ledger edited", []domain.Transaction{changedA, b}, []domain.Transaction{a, b}, finance.DirectionToWorkbook},
		{"workbook edited", []domain.Transaction{a, b}, []domain.Transaction{changedA, b}, finance.DirectionToLedger},
		{"ledger gained a row", []domain.Transaction{a, b, added}, []domain.Transaction{a, b}, finance.DirectionToWorkbook},
		{"workbook lost a row", []domain.Transaction{a, b}, []domain.Transaction{a}, finance.DirectionToLedger},
		// Both sides moved. The engine does not guess which one is right, even
		// when the changes touch different rows and a merge would "obviously" be
		// safe — that is the judgement that eventually loses a transaction.
		{"both sides moved", []domain.Transaction{changedA, b}, []domain.Transaction{a}, finance.DirectionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recs []finance.Record
			for _, tx := range tt.ledger {
				recs = append(recs, recordOf(t, tx, 1))
			}
			plan := finance.Diff(recs, tt.workbook, baseline)
			if plan.Direction != tt.want {
				t.Errorf("Direction = %v, want %v (ledger %+v, workbook %+v)",
					plan.Direction, tt.want, plan.Ledger, plan.Workbook)
			}
		})
	}
}

func TestDiff_reportsWhatMoved(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	b := expenseTx(t, "01B", 30, 50000, "Транспорт", "")
	baseline := stateOf(t, a, b)

	changedA := expenseTx(t, "01A", 29, 99900, "Еда", "хлеб")
	added := expenseTx(t, "01C", 31, 10000, "Еда", "новое")

	plan := finance.Diff(
		[]finance.Record{recordOf(t, changedA, 1), recordOf(t, added, 1)},
		[]domain.Transaction{a, b},
		baseline,
	)
	if plan.Direction != finance.DirectionToWorkbook {
		t.Fatalf("Direction = %v, want ToWorkbook", plan.Direction)
	}
	if got := plan.Ledger.Modified; len(got) != 1 || got[0] != "01A" {
		t.Errorf("Modified = %v, want [01A]", got)
	}
	if got := plan.Ledger.Added; len(got) != 1 || got[0] != "01C" {
		t.Errorf("Added = %v, want [01C]", got)
	}
	if got := plan.Ledger.Removed; len(got) != 1 || got[0] != "01B" {
		t.Errorf("Removed = %v, want [01B]", got)
	}
}

// Without a baseline there is nothing to measure change against. Adopting the
// current state is only safe when the two sides already agree; if they differ,
// which one is ahead is exactly the question that cannot be answered.
func TestDiff_withoutABaseline(t *testing.T) {
	a := expenseTx(t, "01A", 29, 20245, "Еда", "хлеб")
	other := expenseTx(t, "01A", 29, 88800, "Еда", "хлеб")

	agree := finance.Diff(
		[]finance.Record{recordOf(t, a, 1)},
		[]domain.Transaction{a},
		finance.SyncState{},
	)
	if agree.Direction != finance.DirectionNone {
		t.Errorf("Direction = %v, want None when both sides already match", agree.Direction)
	}

	differ := finance.Diff(
		[]finance.Record{recordOf(t, a, 1)},
		[]domain.Transaction{other},
		finance.SyncState{},
	)
	if differ.Direction != finance.DirectionConflict {
		t.Errorf("Direction = %v, want Conflict when there is no baseline and the sides differ", differ.Direction)
	}
}

// Lists are sorted so a conflict report reads the same way twice and can be
// diffed against an earlier one.
func TestDiff_listsAreSorted(t *testing.T) {
	a := expenseTx(t, "01A", 29, 100, "Еда", "")
	b := expenseTx(t, "01B", 29, 100, "Еда", "")
	c := expenseTx(t, "01C", 29, 100, "Еда", "")
	plan := finance.Diff(
		[]finance.Record{recordOf(t, c, 1), recordOf(t, a, 1), recordOf(t, b, 1)},
		nil,
		finance.SyncState{Rows: map[string]string{}},
	)
	want := []string{"01A", "01B", "01C"}
	if len(plan.Ledger.Added) != len(want) {
		t.Fatalf("Added = %v, want %v", plan.Ledger.Added, want)
	}
	for i := range want {
		if plan.Ledger.Added[i] != want[i] {
			t.Fatalf("Added = %v, want %v", plan.Ledger.Added, want)
		}
	}
}

// A ledger holding two records with one id cannot be synced against anything —
// the workbook row it matches is ambiguous.
func TestDiff_duplicateIDIsAConflict(t *testing.T) {
	a := expenseTx(t, "01A", 29, 100, "Еда", "")
	dup := expenseTx(t, "01A", 30, 200, "Еда", "")
	plan := finance.Diff(
		[]finance.Record{recordOf(t, a, 1), recordOf(t, dup, 1)},
		[]domain.Transaction{a},
		stateOf(t, a),
	)
	if plan.Direction != finance.DirectionConflict {
		t.Errorf("Direction = %v, want Conflict for a duplicated id", plan.Direction)
	}
}
