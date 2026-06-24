package domain

import "errors"

// ErrInvalidReadState is returned when a read-state value is not canonical.
var ErrInvalidReadState = errors.New("invalid read state")

// ReadState is a value object for whether an entry has been read. It is
// immutable; construct it via NewReadState.
type ReadState struct {
	value string
}

var canonicalReadStates = map[string]struct{}{
	"read":   {},
	"unread": {},
}

// NewReadState validates raw against the canonical set and returns a ReadState.
func NewReadState(raw string) (ReadState, error) {
	if err := validateEnum(raw, canonicalReadStates, ErrInvalidReadState); err != nil {
		return ReadState{}, err
	}
	return ReadState{value: raw}, nil
}

// String returns the canonical read-state value.
func (r ReadState) String() string {
	return r.value
}
