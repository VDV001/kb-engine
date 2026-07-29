package finance

import (
	"fmt"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ApplyToLedger returns the ledger as it stands once the workbook is taken as
// authoritative: rows whose content differs advance one revision, rows the
// ledger has never seen arrive at revision 1, and rows the workbook no longer
// has are dropped.
//
// The comparison is against the workbook, not against the baseline. Applying
// makes one side match the other, and a decision to apply may have come from
// --resolve rather than from the diff — in which case the baseline says nothing
// useful about what has to be rewritten.
//
// A row that already matches is carried over untouched, same revision and same
// timestamp. Bumping everything on every sync would make the counter
// meaningless and rewrite the whole file to record one edit.
func ApplyToLedger(recs []Record, workbook []domain.Transaction, at time.Time) ([]Record, error) {
	existing := make(map[string]Record, len(recs))
	for _, r := range recs {
		existing[r.Transaction().ID()] = r
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
		case Fingerprint(prev.Transaction()) != Fingerprint(tx):
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

// ToWorkbook returns what it takes to make the workbook match the ledger: the
// transactions to write, and the ids whose rows should be cleared.
//
// Like ApplyToLedger, this compares the two sides rather than consulting the
// baseline, so a forced resolution leaves the files actually agreeing instead
// of only appearing to.
func ToWorkbook(recs []Record, workbook []domain.Transaction) (upserts []domain.Transaction, removals []string) {
	inWorkbook := make(map[string]string, len(workbook))
	for _, tx := range workbook {
		inWorkbook[tx.ID()] = Fingerprint(tx)
	}

	inLedger := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		tx := r.Transaction()
		inLedger[tx.ID()] = struct{}{}
		if was, there := inWorkbook[tx.ID()]; !there || was != Fingerprint(tx) {
			upserts = append(upserts, tx)
		}
	}

	for _, tx := range workbook {
		if _, kept := inLedger[tx.ID()]; !kept {
			removals = append(removals, tx.ID())
		}
	}
	slices.Sort(removals)
	return upserts, removals
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
