package domain

import (
	"errors"
	"fmt"
	"maps"
)

// ErrDuplicateID is returned when an entry with an already-present id is added.
var ErrDuplicateID = errors.New("duplicate entry id")

// Catalog is the aggregate of KB entries. It enforces that ids are unique.
// Construct it via NewCatalog; the zero value is not usable.
type Catalog struct {
	entries        []Entry
	byID           map[int]int // id -> index into entries
	categoryLabels map[string]string
	tagLabels      map[string]string
}

// CatalogOption configures a Catalog at construction time.
type CatalogOption func(*Catalog)

// WithCategoryLabels attaches the catalog's own naming of its categories: the
// key an entry stores against the name a person reads. The map is copied, so a
// caller that keeps editing its own copy cannot reshape a built catalog.
func WithCategoryLabels(labels map[string]string) CatalogOption {
	return func(c *Catalog) {
		c.categoryLabels = maps.Clone(labels)
	}
}

// WithTagLabels attaches readable names for tag keys. Only tags whose key is
// not already readable need one — the catalog describes 24 of them against
// nearly four thousand keys, so most tags legitimately have none.
func WithTagLabels(labels map[string]string) CatalogOption {
	return func(c *Catalog) {
		c.tagLabels = maps.Clone(labels)
	}
}

// NewCatalog builds a Catalog from entries, rejecting duplicate ids. A nil or
// empty slice yields an empty catalog.
func NewCatalog(entries []Entry, opts ...CatalogOption) (*Catalog, error) {
	c := &Catalog{byID: make(map[int]int, len(entries))}
	for _, opt := range opts {
		opt(c)
	}
	for _, e := range entries {
		if err := c.Add(e); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// CategoryLabels returns a copy of how the catalog names its categories: the
// key an entry stores against the name a person reads. A category the catalog
// has not described is simply absent — what to show instead is the caller's
// decision, and inventing a name here would hide the gap.
func (c *Catalog) CategoryLabels() map[string]string {
	// Не maps.Clone: он вернул бы nil у каталога без словаря, а вызывающий
	// вправе писать в полученную карту — в nil-карту запись паникует.
	out := make(map[string]string, len(c.categoryLabels))
	maps.Copy(out, c.categoryLabels)
	return out
}

// TagLabels returns a copy of the readable names for tag keys. A tag without
// one is absent rather than named after itself: whether to fall back to the
// key is the view's decision.
func (c *Catalog) TagLabels() map[string]string {
	out := make(map[string]string, len(c.tagLabels))
	maps.Copy(out, c.tagLabels)
	return out
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
