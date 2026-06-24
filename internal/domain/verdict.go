package domain

import "errors"

// ErrInvalidVerdict is returned when a verdict value is not one of the
// canonical values.
var ErrInvalidVerdict = errors.New("invalid verdict")

// Verdict is a value object for the review verdict on an entry. It is immutable
// and always holds one of the canonical values; construct it via NewVerdict.
//
// Canonical values are English lowercase. Legacy encodings found in stored data
// (e.g. "KEEP", "на подумать") are normalized by the loader before reaching the
// domain, which stays strict.
type Verdict struct {
	value string
}

var canonicalVerdicts = map[string]struct{}{
	"keep":             {},
	"napodumat":        {},
	"skip":             {},
	"skip-unavailable": {},
}

// NewVerdict validates raw against the canonical set and returns a Verdict.
func NewVerdict(raw string) (Verdict, error) {
	if err := validateEnum(raw, canonicalVerdicts, ErrInvalidVerdict); err != nil {
		return Verdict{}, err
	}
	return Verdict{value: raw}, nil
}

// String returns the canonical verdict value.
func (v Verdict) String() string {
	return v.value
}
