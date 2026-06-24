package domain

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidCategory is returned when a category value is not well-formed.
var ErrInvalidCategory = errors.New("invalid category")

// Category is a value object for an entry's category. The set of categories is
// open (it grows over time), so it is not a closed enum; instead the value is
// validated to be a non-empty lowercase kebab-case token. Construct it via
// NewCategory.
type Category struct {
	value string
}

// kebab-case: lowercase alphanumeric words joined by single hyphens, no leading
// or trailing hyphen.
var categoryPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// NewCategory validates raw and returns a Category.
func NewCategory(raw string) (Category, error) {
	if !categoryPattern.MatchString(raw) {
		return Category{}, fmt.Errorf("%w: %q", ErrInvalidCategory, raw)
	}
	return Category{value: raw}, nil
}

// String returns the category value.
func (c Category) String() string {
	return c.value
}
