package finance

import (
	"github.com/daniil/kb-engine/internal/domain"
)

// Repeat is a workbook row that repeats an entry the ledger already holds, and
// the entry it repeats.
type Repeat struct {
	Row      domain.Transaction
	Existing Record
}

// RepeatsFromWorkbook finds rows the workbook carries twice: once as an entry
// the engine wrote, and once as a row put into the cells directly.
//
// A row written past the engine has no id, so the two cannot be matched the
// usual way and the sync reads the second one as new — which is exactly how the
// ledger ends up holding the same expense twice.
//
// Only rows the ledger does not know by id are considered. A row carrying its
// own id is the same entry, not a repeat; treating it as one would flag every
// row the engine itself had written.
func RepeatsFromWorkbook(recs []Record, workbook []domain.Transaction) []Repeat {
	known := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		known[r.Transaction().ID()] = struct{}{}
	}

	var out []Repeat
	for _, row := range workbook {
		if _, ok := known[row.ID()]; ok {
			continue
		}
		// The same comparison the write-time guard uses, so a repeat is one thing
		// in this engine rather than two definitions that drift apart.
		if dup := Duplicate(recs, AddParams{
			Kind:        row.Kind(),
			Date:        row.Date(),
			Amount:      row.Amount(),
			Category:    row.Category(),
			Subcategory: row.Subcategory(),
			Place:       row.Place(),
			Description: row.Description(),
			Source:      row.Source(),
			Account:     row.Account(),
		}); dup != nil {
			out = append(out, Repeat{Row: row, Existing: *dup})
		}
	}
	return out
}
