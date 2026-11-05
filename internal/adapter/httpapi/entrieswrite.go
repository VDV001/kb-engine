package httpapi

import (
	"context"
	"errors"
)

var ErrBadWrite = errors.New("bad write")

type EntryAdd struct {
	Title, Category, File, URL, Verdict, Author, Description, Lifecycle, Source string
	Tags                                                                        []string
}

type EntryPatch struct {
	Lifecycle, Verdict, Category, Title, Description, Notes, Author, URL *string
	AddTags, RemoveTags                                                  []string
}

type AddedEntry struct {
	ID      int
	Title   string
	Already bool
}

type CatalogWriter interface {
	AddEntry(ctx context.Context, a EntryAdd) (AddedEntry, error)
	EditEntry(ctx context.Context, id int, p EntryPatch) error
}

func WithCatalogWriter(CatalogWriter) Option { return func(*options) {} }
