package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidScope is returned for a scope name the server does not know.
var ErrInvalidScope = errors.New("invalid scope")

// Scope is what a person's token may do (#701): ScopeWrite — все, как всегда;
// ScopeRead — только читать. Нулевое значение — запись: человек без записи об
// области работает как раньше.
type Scope struct{ raw string }

// Known scopes.
var (
	ScopeWrite = Scope{}
	ScopeRead  = Scope{raw: "read"}
)

// NewScope parses a scope name: "read", "write" or "" (запись).
func NewScope(s string) (Scope, error) {
	switch s {
	case "read":
		return ScopeRead, nil
	case "write", "":
		return ScopeWrite, nil
	}
	return Scope{}, fmt.Errorf("%w: %q (use read or write)", ErrInvalidScope, s)
}

// String returns "read" or "write".
func (s Scope) String() string {
	if s == ScopeRead {
		return "read"
	}
	return "write"
}

// AllowsWrite reports whether the token may change data.
func (s Scope) AllowsWrite() bool { return s != ScopeRead }
