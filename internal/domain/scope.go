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
// stub: filled in by the next commit.
func NewScope(s string) (Scope, error) { return Scope{}, nil }

// String returns "read" or "write".
func (s Scope) String() string { return "write" }

// AllowsWrite reports whether the token may change data.
func (s Scope) AllowsWrite() bool { return true }

var _ = fmt.Sprint
