package ingest

import (
	"errors"
	"fmt"
	"maps"
	"slices"
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
