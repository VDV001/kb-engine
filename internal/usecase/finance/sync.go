package finance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Direction is which way a sync would move data.
type Direction int

const (
	// DirectionNone means the two sides already agree.
	DirectionNone Direction = iota
	// DirectionToWorkbook means the ledger moved and the workbook has not.
	DirectionToWorkbook
	// DirectionToLedger means the workbook moved and the ledger has not.
	DirectionToLedger
	// DirectionConflict means both sides moved, or the question cannot be
	// answered at all. Nothing is written.
	DirectionConflict
)

func (d Direction) String() string {
	switch d {
	case DirectionNone:
		return "none"
	case DirectionToWorkbook:
		return "ledger → workbook"
	case DirectionToLedger:
		return "workbook → ledger"
	case DirectionConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// SyncState is the snapshot left behind by the last successful sync: what every
// row looked like when the two sides last agreed.
//
// Content hashes rather than modification times. mtime lies after a checkout, a
// copy or a restore from backup, and a sync that trusts it moves the wrong way
// silently.
type SyncState struct {
	SyncedAt time.Time         `json:"synced_at"`
	Rows     map[string]string `json:"rows"`
}

// Side is what changed on one side of the sync since the baseline.
type Side struct {
	Added    []string
	Modified []string
	Removed  []string
}

// Moved reports whether this side changed at all.
func (s Side) Moved() bool {
	return len(s.Added)+len(s.Modified)+len(s.Removed) > 0
}

// Plan is the answer to "what would a sync do", including the answer "stop".
type Plan struct {
	Direction Direction
	Ledger    Side
	Workbook  Side
	// Reason is filled in when the direction is a conflict, and says which of
	// the several ways to conflict this is.
	Reason string
}

// fingerprintSep is a byte that cannot appear in a field, so joining is not
// ambiguous: "аб"+"в" and "а"+"бв" must not hash alike.
const fingerprintSep = "\x1f"

// Fingerprint returns a content hash of a transaction — everything about it
// except its identity.
//
// The id is left out on purpose: it is the key the fingerprint is stored under,
// so including it would make every comparison compare a thing with itself.
func Fingerprint(tx domain.Transaction) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		tx.Kind(),
		tx.Date().Format(time.DateOnly),
		tx.Amount().String(),
		tx.Category(),
		tx.Subcategory(),
		tx.Place(),
		tx.Description(),
		tx.Source(),
	}, fingerprintSep)))
	// Half the digest. A ledger holds hundreds of rows, not billions, and the
	// state file stays readable at this width.
	return hex.EncodeToString(sum[:16])
}

// Diff works out what a sync would do, given both sides and the baseline they
// last agreed on.
//
// It decides and reports; it never writes. When both sides moved it returns a
// conflict rather than merging, even where the changes touch different rows and
// a merge would look safe — that judgement is the one that eventually loses a
// transaction.
func Diff(ledger []Record, workbook []domain.Transaction, st SyncState) Plan {
	ledgerRows, err := fingerprintRecords(ledger)
	if err != nil {
		return Plan{Direction: DirectionConflict, Reason: "ledger: " + err.Error()}
	}
	workbookRows, err := fingerprintTransactions(workbook)
	if err != nil {
		return Plan{Direction: DirectionConflict, Reason: "workbook: " + err.Error()}
	}

	plan := Plan{
		Ledger:   compare(ledgerRows, st.Rows),
		Workbook: compare(workbookRows, st.Rows),
	}

	// No baseline: there is nothing to measure change against. Adopting the
	// current state is safe only when the two sides already agree; when they
	// differ, which one is ahead is exactly the unanswerable question.
	if len(st.Rows) == 0 {
		if maps.Equal(ledgerRows, workbookRows) {
			plan.Direction = DirectionNone
			return plan
		}
		plan.Direction = DirectionConflict
		plan.Reason = "no baseline to compare against and the two sides differ"
		return plan
	}

	switch {
	case plan.Ledger.Moved() && plan.Workbook.Moved():
		plan.Direction = DirectionConflict
		plan.Reason = "both sides changed since the last sync"
	case plan.Ledger.Moved():
		plan.Direction = DirectionToWorkbook
	case plan.Workbook.Moved():
		plan.Direction = DirectionToLedger
	default:
		plan.Direction = DirectionNone
	}
	return plan
}

func fingerprintRecords(recs []Record) (map[string]string, error) {
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		tx := r.Transaction()
		if _, dup := out[tx.ID()]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, tx.ID())
		}
		out[tx.ID()] = Fingerprint(tx)
	}
	return out, nil
}

func fingerprintTransactions(txs []domain.Transaction) (map[string]string, error) {
	out := make(map[string]string, len(txs))
	for _, tx := range txs {
		if _, dup := out[tx.ID()]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, tx.ID())
		}
		out[tx.ID()] = Fingerprint(tx)
	}
	return out, nil
}

// compare reports how one side differs from the baseline. Lists come out sorted
// so a conflict report reads the same way twice.
func compare(now, baseline map[string]string) Side {
	var s Side
	for id, fp := range now {
		was, existed := baseline[id]
		switch {
		case !existed:
			s.Added = append(s.Added, id)
		case was != fp:
			s.Modified = append(s.Modified, id)
		}
	}
	for id := range baseline {
		if _, still := now[id]; !still {
			s.Removed = append(s.Removed, id)
		}
	}
	slices.Sort(s.Added)
	slices.Sort(s.Modified)
	slices.Sort(s.Removed)
	return s
}
