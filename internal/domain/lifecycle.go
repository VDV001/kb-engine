// Package domain holds the KB engine's entities and value objects.
// It has no I/O and no dependency on infrastructure.
package domain

import "errors"

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
	if err := validateEnum(raw, canonicalLifecycles, ErrInvalidLifecycle); err != nil {
		return Lifecycle{}, err
	}
	return Lifecycle{value: raw}, nil
}

// String returns the canonical lifecycle value.
func (l Lifecycle) String() string {
	return l.value
}

const (
	lifecycleOutdated   = "outdated"
	lifecycleCanonical  = "canonical"
	lifecycleSuperseded = "superseded"
	lifecycleDeadEnd    = "dead-end"
)

// IsTerminal reports whether the entry has already been dealt with and needs no
// further lifecycle decision: outdated says exactly that, dead-end says the
// trail stops here, superseded says another entry took over.
//
// Audits ask this before proposing work. A suggestion that repeats a decision
// already made is not advice: on the live catalog 49 of 52 «outdated candidates»
// were entries long since filed as dead-end, and acting on that list would have
// replaced a specific state with a vaguer one.
func (l Lifecycle) IsTerminal() bool {
	switch l.value {
	case lifecycleOutdated, lifecycleDeadEnd, lifecycleSuperseded:
		return true
	}
	return false
}

// IsOutdated reports whether the lifecycle is the outdated state.
func (l Lifecycle) IsOutdated() bool {
	return l.value == lifecycleOutdated
}

// IsCanonical reports whether the lifecycle is the canonical state.
func (l Lifecycle) IsCanonical() bool {
	return l.value == lifecycleCanonical
}

// IsSuperseded reports whether the entry has been replaced by another one and
// is kept only for the record.
func (l Lifecycle) IsSuperseded() bool {
	return l.value == lifecycleSuperseded
}
