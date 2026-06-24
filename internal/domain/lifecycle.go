// Package domain holds the KB engine's entities and value objects.
// It has no I/O and no dependency on infrastructure.
package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidLifecycle is returned when a lifecycle value is not one of the
// canonical states.
var ErrInvalidLifecycle = errors.New("invalid lifecycle")

// Lifecycle is a value object for an entry's lifecycle state. It is immutable
// and always holds one of the canonical values; construct it via NewLifecycle.
type Lifecycle struct {
	value string
}

var canonicalLifecycles = map[string]struct{}{
	"active":     {},
	"canonical":  {},
	"outdated":   {},
	"superseded": {},
	"dead-end":   {},
}

// NewLifecycle validates raw against the canonical set and returns a Lifecycle.
// Matching is strict (case-sensitive); normalizing messy input is a loader
// concern, kept out of the domain.
func NewLifecycle(raw string) (Lifecycle, error) {
	if _, ok := canonicalLifecycles[raw]; !ok {
		return Lifecycle{}, fmt.Errorf("%w: %q", ErrInvalidLifecycle, raw)
	}
	return Lifecycle{value: raw}, nil
}

// String returns the canonical lifecycle value.
func (l Lifecycle) String() string {
	return l.value
}
