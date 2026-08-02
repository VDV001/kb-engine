package finance

import (
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Duplicate returns the record a new entry would repeat, or nil when it repeats
// nothing.
//
// Written as a question asked at write time rather than a report produced later.
// A repeated expense is the failure that happens on its own — a session drops
// half-way, the person types it again, and the book carries the same purchase twice.
// Found on the dashboard a week later it is nearly unresolvable: nobody can say
// which of the two was the real purchase.
//
// Two similar expenses on one day are ordinary life, not an error: two minute
// packages on a scooter, two rides on the same fare. What tells them apart is
// the note, so a differing note means these are two purchases and the engine
// says nothing.
func Duplicate(existing []Record, p AddParams) *Record {
	// The date is resolved the way Add resolves it. Comparing the raw field
	// would switch the check off in the most common case of all — the one where
	// nobody types a date.
	want := p.Date
	if want.IsZero() {
		want = domain.Day(time.Now())
	}

	for i := range existing {
		tx := existing[i].Transaction()
		if tx.Kind() != p.Kind || !tx.Date().Equal(want) || tx.Amount() != p.Amount {
			continue
		}
		if same(tx.Category(), p.Category) && same(tx.Subcategory(), p.Subcategory) &&
			same(tx.Place(), p.Place) && same(tx.Description(), p.Description) &&
			same(tx.Source(), p.Source) && same(tx.Account(), p.Account) {
			return &existing[i]
		}
	}
	return nil
}

// same compares two field values the way a person would: surrounding spaces and
// case are not a difference. «Магнит» and «магнит » are one shop.
func same(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
