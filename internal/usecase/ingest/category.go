package ingest

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrUndeclaredCategory is returned when an entry being added carries a category
// that meta.categories does not declare.
var ErrUndeclaredCategory = errors.New("category is not declared in meta.categories")

// UndeclaredCategoryError names the offending value and the declared set, so the
// caller can say what to write instead. The set travels with the error rather
// than being collected again at the call site: two collectors of one fact drift.
type UndeclaredCategoryError struct {
	Title    string
	Category string
	Declared []string
}

func (e *UndeclaredCategoryError) Error() string {
	return fmt.Sprintf("entry %q: %v: %q", e.Title, ErrUndeclaredCategory, e.Category)
}

func (e *UndeclaredCategoryError) Unwrap() error { return ErrUndeclaredCategory }

// declaredCategories returns the sorted keys of the catalog's category
// dictionary.
func declaredCategories(labels map[string]string) []string {
	return slices.Sorted(maps.Keys(labels))
}

// checkCategory refuses an entry whose category the catalog does not declare.
//
// A category outside meta.categories is not untidy, it is broken: filters still
// show the entry, the sidebar still draws a box, and the box gets a technical
// label because nothing describes it. The audit says the same thing afterwards
// — this says it before the write, which is the difference between a rule and
// a report.
//
// An empty dictionary is not a violation but an inability to check: the caller
// is told through Report.CategoriesUnchecked rather than being refused, because
// a catalog may legitimately be built up before its dictionary is.
func checkCategory(declared map[string]string, p domain.EntryParams) error {
	if len(declared) == 0 {
		return nil
	}
	if _, ok := declared[p.Category.String()]; ok {
		return nil
	}
	return &UndeclaredCategoryError{
		Title:    p.Title,
		Category: p.Category.String(),
		Declared: declaredCategories(declared),
	}
}
