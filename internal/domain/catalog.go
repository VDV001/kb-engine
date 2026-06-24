package domain

import (
	"errors"
	"fmt"
)

// ErrDuplicateID is returned when an entry with an already-present id is added.
var ErrDuplicateID = errors.New("duplicate entry id")

// Catalog is the aggregate of KB entries. It enforces that ids are unique.
// Construct it via NewCatalog; the zero value is not usable.
type Catalog struct {
	entries []Entry
	byID    map[int]int // id -> index into entries
}

// NewCatalog builds a Catalog from entries, rejecting duplicate ids. A nil or
// empty slice yields an empty catalog.
func NewCatalog(entries []Entry) (*Catalog, error) {
	c := &Catalog{byID: make(map[int]int, len(entries))}
	for _, e := range entries {
		if err := c.Add(e); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Add appends an entry, returning ErrDuplicateID if its id is already present.
func (c *Catalog) Add(e Entry) error {
	if _, exists := c.byID[e.ID()]; exists {
		return fmt.Errorf("%w: %d", ErrDuplicateID, e.ID())
	}
	c.byID[e.ID()] = len(c.entries)
	c.entries = append(c.entries, e)
	return nil
}

// Find returns the entry with the given id and whether it was found.
func (c *Catalog) Find(id int) (Entry, bool) {
	idx, ok := c.byID[id]
	if !ok {
		return Entry{}, false
	}
	return c.entries[idx], true
}

// Len returns the number of entries.
func (c *Catalog) Len() int { return len(c.entries) }

// NextID returns the id to assign to the next entry: one past the highest
// present id, or 1 for an empty catalog. ids are not reused.
func (c *Catalog) NextID() int {
	highest := 0
	for _, e := range c.entries {
		if e.id > highest {
			highest = e.id
		}
	}
	return highest + 1
}

// Entries returns a copy of the entries, so callers cannot mutate the catalog's
// backing array.
func (c *Catalog) Entries() []Entry {
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	return out
}
