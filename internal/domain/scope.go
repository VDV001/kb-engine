package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidScope is returned for a scope name the server does not know.
var ErrInvalidScope = errors.New("invalid scope")

// Scope is what a person's token may do (#701, #703): ScopeWrite — все, как всегда;
// ScopeAppend — читать и добавлять записи (POST /api/entries), но не менять и не
// удалять; ScopeRead — только читать. Нулевое значение — запись: человек без
// записи об области работает как раньше.
type Scope struct{ raw string }

// Known scopes.
var (
	ScopeWrite  = Scope{}
	ScopeRead   = Scope{raw: "read"}
	ScopeAppend = Scope{raw: "append"}
)

// NewScope parses a scope name: "read", "append", "write" or "" (запись).
func NewScope(s string) (Scope, error) {
	switch s {
	case "read":
		return ScopeRead, nil
	case "append":
		return ScopeAppend, nil
	case "write", "":
		return ScopeWrite, nil
	}
	return Scope{}, fmt.Errorf("%w: %q (use read, append or write)", ErrInvalidScope, s)
}

// String returns "read", "append" or "write".
func (s Scope) String() string {
	switch s {
	case ScopeRead:
		return "read"
	case ScopeAppend:
		return "append"
	}
	return "write"
}

// AllowsWrite reports whether the token may change anything: только полная область.
func (s Scope) AllowsWrite() bool { return s == ScopeWrite }

// appendPath is the only place a token with ScopeAppend may POST to: добавление
// записи. Путь сравнивается как его видит маршрутизатор (раскодированный).
const appendPath = "/api/entries"

// Allows reports whether a request with this method and path may run. Чтение
// (GET, HEAD) разрешено любой области; добавление - только append и write.
func (s Scope) Allows(method, path string) bool {
	if s == ScopeWrite || method == "GET" || method == "HEAD" {
		return true
	}
	return s == ScopeAppend && method == "POST" && path == appendPath
}
