// Package finance is the use case layer for the personal ledger. It owns the
// application policy — how a spreadsheet row becomes a stored record, what an
// omitted date means, what order the file is kept in — while validation of the
// transaction itself stays in the domain.
//
// It is pure: every function takes already-loaded records and returns the ones
// to persist. Reading and writing files is the caller's concern, which keeps
// the clock and the id source as parameters rather than ambient state.
package finance

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrInvalidRecord is returned when the revision metadata around a transaction
// is not usable.
var ErrInvalidRecord = errors.New("invalid record")

// ErrDuplicateID is returned when two records would carry the same identity.
var ErrDuplicateID = errors.New("duplicate transaction id")

// Record is a stored transaction together with the metadata a two-way sync
// needs: which revision this is, and when it was last written.
//
// Rev and UpdatedAt live here rather than in the domain because they say
// nothing about the money — they exist only so two copies of the same ledger
// can be compared. The domain stays about transactions.
type Record struct {
	tx        domain.Transaction
	rev       int
	updatedAt time.Time
}

// NewRecord validates the revision metadata and returns a Record.
func NewRecord(tx domain.Transaction, rev int, updatedAt time.Time) (Record, error) {
	if rev < 1 {
		return Record{}, fmt.Errorf("%w: revision must start at 1, got %d", ErrInvalidRecord, rev)
	}
	if updatedAt.IsZero() {
		return Record{}, fmt.Errorf("%w: updatedAt is required", ErrInvalidRecord)
	}
	return Record{tx: tx, rev: rev, updatedAt: updatedAt}, nil
}

// Transaction returns the stored transaction.
func (r Record) Transaction() domain.Transaction { return r.tx }

// Rev returns the revision counter, starting at 1.
func (r Record) Rev() int { return r.rev }

// UpdatedAt returns when this revision was written.
func (r Record) UpdatedAt() time.Time { return r.updatedAt }

// Import turns transactions read out of the spreadsheet into first-revision
// records, replacing each positional id with one from newID. Everything else
// about the transaction is carried over untouched.
//
// A repeated id aborts the whole import: half a ledger is worse than none, and
// the alternative is two rows quietly collapsing into one at the next load.
func Import(txs []domain.Transaction, newID func() string, at time.Time) ([]Record, error) {
	seen := make(map[string]struct{}, len(txs))
	out := make([]Record, 0, len(txs))
	for _, tx := range txs {
		id := newID()
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, id)
		}
		seen[id] = struct{}{}

		identified, err := tx.WithID(id)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", tx.ID(), err)
		}
		rec, err := NewRecord(identified, 1, at)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", tx.ID(), err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// AddParams carries the values a single new entry needs. Date is optional: a
// zero value means today.
type AddParams struct {
	Kind        string
	Date        time.Time
	Amount      domain.Money
	Category    string
	Subcategory string
	Place       string
	Description string
	Source      string
}

// Add builds a first-revision record for one new entry.
//
// An omitted date resolves to today at midnight UTC, from the injected clock.
// That default is application policy, not a domain rule, and it belongs here so
// that no handler has to reach for the wall clock.
func Add(p AddParams, newID func() string, now func() time.Time) (Record, error) {
	date := p.Date
	if date.IsZero() {
		date = domain.Day(now())
	}

	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID:          newID(),
		Kind:        p.Kind,
		Date:        date,
		Amount:      p.Amount,
		Category:    p.Category,
		Subcategory: p.Subcategory,
		Place:       p.Place,
		Description: p.Description,
		Source:      p.Source,
		Now:         now,
	})
	if err != nil {
		return Record{}, err
	}
	return NewRecord(tx, 1, now())
}

// Sort orders records chronologically, with the id breaking ties, in place.
// The file is read by people and diffed by git, so a stable order matters as
// much as the contents.
func Sort(recs []Record) {
	slices.SortStableFunc(recs, func(a, b Record) int {
		if c := a.tx.Date().Compare(b.tx.Date()); c != 0 {
			return c
		}
		return cmp.Compare(a.tx.ID(), b.tx.ID())
	})
}
