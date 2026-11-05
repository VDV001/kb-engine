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

// errNothingToWrite stops an Update that has nothing to write: запись уже
// есть, или правка ничего не меняет.
var errNothingToWrite = errors.New("nothing to write")

// addParams builds the domain params the way kbengine add does, с теми же
// значениями по умолчанию; источник записи — api.
func addParams(a httpapi.EntryAdd) (domain.EntryParams, error) {
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Category) == "" || strings.TrimSpace(a.URL) == "" {
		return domain.EntryParams{}, fmt.Errorf("%w: нужны title, category и url", httpapi.ErrBadWrite)
	}
	// Теги идут дальше строкой через запятую (как --tags у add): тег с запятой
	// молча стал бы двумя.
	for _, tag := range a.Tags {
		if strings.Contains(tag, ",") {
			return domain.EntryParams{}, fmt.Errorf("%w: тег %q: запятая внутри тега", httpapi.ErrBadWrite, tag)
		}
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
			return nil, refusal(err)
		}
		if rep.Added == 0 {
			out = alreadyThere(cat, a)
			return nil, errNothingToWrite
		}
		out = httpapi.AddedEntry{ID: added[0].ID(), Title: added[0].Title(), CategoryUnchecked: rep.CategoriesUnchecked}
		return catalogjson.AppendEntriesDoc(raw, added)
	})
	if errors.Is(err, errNothingToWrite) {
		return out, nil
	}
	return out, err
}

// refusal turns a rule's refusal into ErrBadWrite, с тем же текстом о
// категории, что у kbengine add.
func refusal(err error) error {
	var undeclared *ingest.UndeclaredCategoryError
	if errors.As(err, &undeclared) {
		return fmt.Errorf("%w: %s", httpapi.ErrBadWrite, categoryRefusal(undeclared))
	}
	return fmt.Errorf("%w: %w", httpapi.ErrBadWrite, err)
}

// alreadyThere names the entry the add found: дедуп ingest сравнивает адреса
// после обрезки пробелов, и здесь сравнение то же.
func alreadyThere(cat *domain.Catalog, a httpapi.EntryAdd) httpapi.AddedEntry {
	out := httpapi.AddedEntry{Title: a.Title, Already: true}
	want := strings.TrimSpace(a.URL)
	for _, e := range cat.Entries() {
		if strings.TrimSpace(e.URL()) == want {
			out.ID, out.Title = e.ID(), e.Title()
		}
	}
	return out
}

// EditEntry applies the patch to one entry and returns how many changed (0
// или 1). Правка без изменений ничего не пишет: новая версия каталога ради
// ничего — тот же «готово без правки», от которого починен set.
//
// ponytail: каталог на каждую попытку разбирается дважды (Decode для проверок
// и SetFieldsDoc для правки). Потолок — два разбора 2 МБ на запись. Путь
// апгрейда — правка поверх уже разобранных записей.
func (s storeCatalogWriter) EditEntry(ctx context.Context, id int, p httpapi.EntryPatch) (int, error) {
	ch, err := patchChanges(p)
	if err != nil {
		return 0, err
	}
	updated := 0
	err = s.w.Update(ctx, func(raw []byte) ([]byte, error) {
		// Ошибка разбора самого каталога — не вина клиента: 500, а не 400.
		cat, err := catalogjson.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		e, ok := cat.Find(id)
		if !ok {
			return nil, fmt.Errorf("%w: %d", httpapi.ErrNoEntry, id)
		}
		if ch.Category != "" {
			if err := ingest.CheckDeclared(cat, e.Title(), ch.Category); err != nil {
				return nil, refusal(err)
			}
		}
		doc, n, err := catalogjson.SetFieldsDoc(raw, []int{id}, ch)
		if err != nil {
			return nil, refusal(err)
		}
		if updated = n; n == 0 {
			return nil, errNothingToWrite
		}
		return doc, nil
	})
	if errors.Is(err, errNothingToWrite) {
		return 0, nil
	}
	return updated, err
}

// patchChanges maps the patch onto catalogjson.Changes. Пустая строка в
// url — «очистить адрес» (ClearURL); в остальных полях пустое значение не
// очищает поле молча, а отказывается: так же, как set не берет пустой флаг.
func patchChanges(p httpapi.EntryPatch) (catalogjson.Changes, error) {
	ch := catalogjson.Changes{AddTags: p.AddTags, RemoveTags: p.RemoveTags}
	if p.URL != nil {
		if *p.URL == "" {
			ch.ClearURL = true
		} else {
			ch.URL = *p.URL
		}
	}
	for name, f := range map[string]struct {
		v   *string
		dst *string
	}{
		"lifecycle": {p.Lifecycle, &ch.Lifecycle}, "verdict": {p.Verdict, &ch.Verdict},
		"category": {p.Category, &ch.Category}, "title": {p.Title, &ch.Title},
		"description": {p.Description, &ch.Description}, "notes": {p.Notes, &ch.Notes},
		"author": {p.Author, &ch.Author},
	} {
		if f.v == nil {
			continue
		}
		if strings.TrimSpace(*f.v) == "" {
			return catalogjson.Changes{}, fmt.Errorf("%w: %s: пустое значение — это поле очистить нельзя", httpapi.ErrBadWrite, name)
		}
		*f.dst = *f.v
	}
	return ch, nil
}
