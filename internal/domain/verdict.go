package domain

import (
	"errors"
	"slices"
)

// ErrInvalidVerdict is returned when a verdict value is not one of the
// canonical values.
var ErrInvalidVerdict = errors.New("invalid verdict")

// Verdict is a value object for the review verdict on an entry. It is immutable
// and always holds one of the canonical values; construct it via NewVerdict.
//
// Canonical values are English lowercase. Legacy encodings found in stored data
// (e.g. "KEEP", "на подумать", "napodumat") are normalized by the loader before
// reaching the domain, which stays strict.
type Verdict struct {
	value string
}

// verdictOrder is the canonical set in triage order: what to do with the
// material, from keeping it to finding it gone. The set is built from it.
var verdictOrder = []string{"keep", "consider", "skip", "skip-unavailable"}

var canonicalVerdicts = setOf(verdictOrder)

// Verdicts returns the canonical verdicts in display order.
func Verdicts() []string { return slices.Clone(verdictOrder) }

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

const verdictSkipUnavailable = "skip-unavailable"

// IsSkipUnavailable reports whether the verdict marks a source that is no
// longer available (HTTP 403 / taken down).
func (v Verdict) IsSkipUnavailable() bool {
	return v.value == verdictSkipUnavailable
}
