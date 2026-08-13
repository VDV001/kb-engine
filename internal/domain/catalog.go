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
	unreadable     []Unreadable
}

// Unreadable — запись, которая в источнике была, а в каталог не попала:
// прочитать её не удалось.
//
// Это факт о каталоге, а не об ошибке разбора, поэтому он живёт здесь: витрина
// обязана сказать, что показывает не всё, и назвать виновную запись. Причина
// хранится строкой — домен не знает, кто и как его собирал.
type Unreadable struct {
	// Index — место записи в источнике. Нужен, когда id прочитать не удалось
	// тоже: «третья запись сверху» это единственный адрес, который тогда есть.
	Index  int
	ID     int
	Reason string
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

// WithUnreadable записывает, каких записей в каталоге нет и почему.
//
// Пустой список и отсутствие списка — одно и то же: «все записи прочитаны».
// А вот непустой означает, что любая витрина показывает неполную базу, и
// молчать об этом нельзя.
func WithUnreadable(bad []Unreadable) CatalogOption {
	return func(c *Catalog) {
		c.unreadable = append([]Unreadable(nil), bad...)
	}
}

// Unreadable returns the entries the source had and the catalog does not.
func (c *Catalog) Unreadable() []Unreadable {
	return append([]Unreadable(nil), c.unreadable...)
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
	// Непрочитанные считаются наравне с прочитанными: их в файле никто не
	// удалял, и выдать новой записи номер, который там уже стоит, значит
	// завести двух хозяев одного id. Запись без разобранного id (0) на счёт не
	// влияет — про неё неизвестно ничего, кроме места в файле.
	for _, u := range c.unreadable {
		if u.ID > highest {
			highest = u.ID
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
