package domain

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ErrInvalidLocation is returned when a reference points somewhere an entry
// cannot point: an address that is not a web address, or a write-up path that
// leaves the knowledge base.
var ErrInvalidLocation = errors.New("invalid location")

// ExternalURL is where the original material lives. The catalog already has
// entries whose url field holds a file path — a mix-up this type exists to stop
// from happening again.
type ExternalURL struct{ raw string }

// NewExternalURL validates that raw is an http(s) address with a host.
func NewExternalURL(raw string) (ExternalURL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ExternalURL{}, fmt.Errorf("%w: url is empty", ErrInvalidLocation)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ExternalURL{}, fmt.Errorf("%w: url %q does not parse: %w", ErrInvalidLocation, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ExternalURL{}, fmt.Errorf("%w: url %q is not http(s) — a file path belongs in file, not url", ErrInvalidLocation, raw)
	}
	if u.Host == "" {
		return ExternalURL{}, fmt.Errorf("%w: url %q has no host", ErrInvalidLocation, raw)
	}
	return ExternalURL{raw: trimmed}, nil
}

// String returns the address.
func (u ExternalURL) String() string { return u.raw }

// NotesPath is where the write-up for an entry lives, relative to the knowledge
// base root. An absolute path or one climbing above the root would name a file
// nobody else has.
type NotesPath struct{ raw string }

// NewNotesPath validates that raw is a relative path that stays inside the base.
func NewNotesPath(raw string) (NotesPath, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return NotesPath{}, fmt.Errorf("%w: file is empty", ErrInvalidLocation)
	}
	if path.IsAbs(trimmed) || strings.HasPrefix(trimmed, "~") {
		return NotesPath{}, fmt.Errorf("%w: file %q is absolute — paths are relative to the knowledge base", ErrInvalidLocation, raw)
	}
	cleaned := path.Clean(trimmed)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return NotesPath{}, fmt.Errorf("%w: file %q leaves the knowledge base", ErrInvalidLocation, raw)
	}
	return NotesPath{raw: cleaned}, nil
}

// String returns the path.
func (p NotesPath) String() string { return p.raw }
