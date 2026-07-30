package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// entryWithID builds a valid article entry with the given id.
func entryWithID(t *testing.T, id int) domain.Entry {
	t.Helper()
	p := validArticle(t)
	p.ID = id
	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("setup entry %d: %v", id, err)
	}
	return e
}

func TestNewCatalog(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		c, err := domain.NewCatalog(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Len() != 0 {
			t.Errorf("Len() = %d, want 0", c.Len())
		}
	})

	t.Run("from entries", func(t *testing.T) {
		c, err := domain.NewCatalog([]domain.Entry{entryWithID(t, 1), entryWithID(t, 2)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Len() != 2 {
			t.Errorf("Len() = %d, want 2", c.Len())
		}
	})

	t.Run("rejects duplicate id", func(t *testing.T) {
		_, err := domain.NewCatalog([]domain.Entry{entryWithID(t, 1), entryWithID(t, 1)})
		if !errors.Is(err, domain.ErrDuplicateID) {
			t.Fatalf("err = %v, want ErrDuplicateID", err)
		}
	})
}

func TestCatalog_AddAndFind(t *testing.T) {
	c, err := domain.NewCatalog(nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := c.Add(entryWithID(t, 7)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}

	got, ok := c.Find(7)
	if !ok {
		t.Fatalf("Find(7) not found")
	}
	if got.ID() != 7 {
		t.Errorf("Find(7).ID() = %d, want 7", got.ID())
	}

	if _, ok := c.Find(999); ok {
		t.Errorf("Find(999) = found, want not found")
	}

	if err := c.Add(entryWithID(t, 7)); !errors.Is(err, domain.ErrDuplicateID) {
		t.Errorf("Add duplicate err = %v, want ErrDuplicateID", err)
	}
}

func TestCatalog_NextID(t *testing.T) {
	t.Run("empty catalog starts at 1", func(t *testing.T) {
		c, err := domain.NewCatalog(nil)
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		if got := c.NextID(); got != 1 {
			t.Errorf("NextID() = %d, want 1", got)
		}
	})

	t.Run("returns max id plus one", func(t *testing.T) {
		c, err := domain.NewCatalog([]domain.Entry{entryWithID(t, 3), entryWithID(t, 7), entryWithID(t, 5)})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		if got := c.NextID(); got != 8 {
			t.Errorf("NextID() = %d, want 8", got)
		}
	})

	t.Run("reflects entries added later", func(t *testing.T) {
		c, err := domain.NewCatalog([]domain.Entry{entryWithID(t, 4)})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := c.Add(entryWithID(t, 10)); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if got := c.NextID(); got != 11 {
			t.Errorf("NextID() = %d, want 11", got)
		}
	})
}

func TestCatalog_EntriesIsACopy(t *testing.T) {
	c, err := domain.NewCatalog([]domain.Entry{entryWithID(t, 1)})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Overwriting an element of the returned slice must not reach the catalog's
	// backing array.
	got := c.Entries()
	got[0] = entryWithID(t, 999)

	again := c.Entries()
	if again[0].ID() != 1 {
		t.Errorf("Entries() shares backing array: id = %d, want 1", again[0].ID())
	}
}

// Каталог несёт не только записи, но и словарь своих категорий: ключ вида
// "claude-ecosystem" и человеческое название рядом с ним. Без словаря вид
// вынужден показывать ключ, а ключ — это идентификатор, не имя.
func TestCatalog_categoryLabels(t *testing.T) {
	labels := map[string]string{"local-ai": "Локальный AI: запуск моделей на своём железе"}
	c, err := domain.NewCatalog(nil, domain.WithCategoryLabels(labels))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	const want = "Локальный AI: запуск моделей на своём железе"
	if got := c.CategoryLabels()["local-ai"]; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
	// Неописанная категория просто отсутствует, а не получает выдуманное имя:
	// что показать вместо него, решает вызывающий.
	if got, ok := c.CategoryLabels()["нет-такой"]; ok {
		t.Errorf("unknown category present with %q, want absent", got)
	}
	// Копия в обе стороны: ни правка исходной карты после конструирования, ни
	// правка выданной копии не меняют каталог.
	labels["local-ai"] = "подменено"
	c.CategoryLabels()["local-ai"] = "тоже подменено"
	if got := c.CategoryLabels()["local-ai"]; got != want {
		t.Errorf("label = %q after mutating both maps, want the original", got)
	}
}
