package finance

import (
	"fmt"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ApplyToLedger returns the ledger as it stands once the workbook is taken as
// authoritative: edited rows advance one revision, rows the ledger has never
// seen arrive at revision 1, and rows the workbook no longer has are dropped.
//
// A row neither side touched is carried over untouched — same revision, same
// timestamp. Bumping everything on every sync would make the counter
// meaningless and rewrite the whole file to record one edit.
func ApplyToLedger(recs []Record, workbook []domain.Transaction, plan Plan, at time.Time) ([]Record, error) {
	if plan.Direction == DirectionConflict {
		return nil, fmt.Errorf("refusing to apply a conflicting plan: %s", plan.Reason)
	}

	existing := make(map[string]Record, len(recs))
	for _, r := range recs {
		existing[r.Transaction().ID()] = r
	}
	changed := make(map[string]struct{}, len(plan.Workbook.Modified))
	for _, id := range plan.Workbook.Modified {
		changed[id] = struct{}{}
	}

	out := make([]Record, 0, len(workbook))
	for _, tx := range workbook {
		prev, known := existing[tx.ID()]
		switch {
		case !known:
			rec, err := NewRecord(tx, 1, at)
			if err != nil {
				return nil, fmt.Errorf("adopt %s: %w", tx.ID(), err)
			}
			out = append(out, rec)
		case isChanged(changed, tx.ID()):
			rec, err := NewRecord(tx, prev.Rev()+1, at)
			if err != nil {
				return nil, fmt.Errorf("update %s: %w", tx.ID(), err)
			}
			out = append(out, rec)
		default:
			out = append(out, prev)
		}
	}

	Sort(out)
	return out, nil
}

func isChanged(changed map[string]struct{}, id string) bool {
	_, ok := changed[id]
	return ok
}

// BaselineOf is the state to record once a sync has succeeded: what every row
// looks like now that both sides agree on it.
func BaselineOf(recs []Record, at time.Time) SyncState {
	rows := make(map[string]string, len(recs))
	for _, r := range recs {
		rows[r.Transaction().ID()] = Fingerprint(r.Transaction())
	}
	return SyncState{SyncedAt: at, Rows: rows}
}

// ToWorkbook returns what a ledger-wins sync has to write into the workbook:
// the transactions to upsert, and the ids whose rows should be cleared.
func ToWorkbook(recs []Record, plan Plan) (upserts []domain.Transaction, removals []string) {
	touched := make(map[string]struct{}, len(plan.Ledger.Added)+len(plan.Ledger.Modified))
	for _, id := range slices.Concat(plan.Ledger.Added, plan.Ledger.Modified) {
		touched[id] = struct{}{}
	}
	for _, r := range recs {
		if _, ok := touched[r.Transaction().ID()]; ok {
			upserts = append(upserts, r.Transaction())
		}
	}
	return upserts, slices.Clone(plan.Ledger.Removed)
}
