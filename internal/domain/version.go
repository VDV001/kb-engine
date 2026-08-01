package domain

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidVersion is returned when a raw string is not a three-component
// semantic version.
var ErrInvalidVersion = errors.New("invalid version")

// Version is a value object for the version of an artefact the owner writes and
// versions himself — a standard, an article draft, a course module. It is
// always three numeric components; construct it via NewVersion.
//
// The catalog is a copy of a version that lives in the artefact file itself,
// which is why Compare exists: the audit needs «the copy fell behind», not
// merely «the two differ».
type Version struct {
	major, minor, patch int
}

// NewVersion parses raw as "major.minor.patch". Two components are rejected on
// purpose: "1.0" is how the legacy catalog spelled a default nobody chose, and
// silently widening it to 1.0.0 would turn storage noise into a claim about the
// artefact. Decorations such as a leading "v" belong to a loader, not here.
func NewVersion(raw string) (Version, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: %q must have three components", ErrInvalidVersion, raw)
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("%w: %q has a non-numeric component %q", ErrInvalidVersion, raw, p)
		}
		out[i] = n
	}
	return Version{major: out[0], minor: out[1], patch: out[2]}, nil
}

// String returns the canonical "major.minor.patch" form.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// Compare returns -1 when v precedes other, 0 when they are equal, and 1 when v
// follows other. Comparison is numeric per component, so 1.10.0 follows 1.9.0 —
// string ordering would get that backwards.
func (v Version) Compare(other Version) int {
	if c := cmp.Compare(v.major, other.major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.minor, other.minor); c != 0 {
		return c
	}
	return cmp.Compare(v.patch, other.patch)
}
