package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

// storeCatalogWriter is the API's write path for a catalog in a blob store.
// Правила те же, что у kbengine add и set: параметры собирает artefactParams,
// номер и дедуп дает ingest.Plan, правку — catalogjson.SetFieldsDoc. Разница
// только в том, куда пишется документ: StoreWriter перечитывает каталог при
// конфликте и планирует заново, поэтому одновременные add не делят номер.
type storeCatalogWriter struct {
	w   catalogjson.StoreWriter
	now func() time.Time
}

// errAlreadyThere stops an Update that has nothing to write.
var errAlreadyThere = errors.New("already in the catalog")

// addParams builds the domain params the way kbengine add does, с теми же
// значениями по умолчанию; источник записи — api.
func addParams(a httpapi.EntryAdd) (domain.EntryParams, error) {
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Category) == "" || strings.TrimSpace(a.URL) == "" {
		return domain.EntryParams{}, fmt.Errorf("%w: нужны title, category и url", httpapi.ErrBadWrite)
	}
	lifecycle, source := cmp.Or(a.Lifecycle, "active"), cmp.Or(a.Source, "api")
	params, err := artefactParams(entryFlags{
		title: a.Title, category: a.Category, url: a.URL, verdict: a.Verdict, author: a.Author,
		description: a.Description, tags: strings.Join(a.Tags, ","), lifecycle: lifecycle, source: source,
	})
	if err != nil {
		return domain.EntryParams{}, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
	}
	return params, nil
}

func (s storeCatalogWriter) AddEntry(ctx context.Context, a httpapi.EntryAdd) (httpapi.AddedEntry, error) {
	params, err := addParams(a)
	if err != nil {
		return httpapi.AddedEntry{}, err
	}
	var out httpapi.AddedEntry
	err = s.w.Update(ctx, func(raw []byte) ([]byte, error) {
		cat, err := catalogjson.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		added, rep, err := ingest.Plan(cat, []domain.EntryParams{params}, s.now())
		if err != nil {
			var undeclared *ingest.UndeclaredCategoryError
			if errors.As(err, &undeclared) {
				return nil, fmt.Errorf("%w: %s", httpapi.ErrBadWrite, categoryRefusal(undeclared))
			}
			return nil, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
		}
		if rep.Added == 0 {
			out = alreadyThere(cat, a)
			return nil, errAlreadyThere
		}
		out = httpapi.AddedEntry{ID: added[0].ID(), Title: added[0].Title()}
		return catalogjson.AppendEntriesDoc(raw, added)
	})
	if errors.Is(err, errAlreadyThere) {
		return out, nil
	}
	return out, err
}

// alreadyThere names the entry the add found. Номер — у записи с тем же
// адресом; если дедуп совпал по нормализованному адресу, номер 0.
func alreadyThere(cat *domain.Catalog, a httpapi.EntryAdd) httpapi.AddedEntry {
	out := httpapi.AddedEntry{Title: a.Title, Already: true}
	for _, e := range cat.Entries() {
		if e.URL() == a.URL {
			out.ID, out.Title = e.ID(), e.Title()
		}
	}
	return out
}

func (s storeCatalogWriter) EditEntry(ctx context.Context, id int, p httpapi.EntryPatch) error {
	ch := catalogjson.Changes{
		Lifecycle: deref(p.Lifecycle), Verdict: deref(p.Verdict), Category: deref(p.Category),
		Title: deref(p.Title), Description: deref(p.Description), Notes: deref(p.Notes),
		Author: deref(p.Author), URL: deref(p.URL), AddTags: p.AddTags, RemoveTags: p.RemoveTags,
	}
	return s.w.Update(ctx, func(raw []byte) ([]byte, error) {
		// Отказы SetFieldsDoc — правила правки (неизвестный id, недопустимое
		// значение): 400 с причиной. Разбор самого каталога здесь уже прошел
		// при чтении сервером, иначе до правки дело не дошло бы.
		doc, _, err := catalogjson.SetFieldsDoc(raw, []int{id}, ch)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
		}
		return doc, nil
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
