package tui_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
)

func mustEntry(t *testing.T, id int, title, category string, tags []string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory(category)
	if err != nil {
		t.Fatalf("category %q: %v", category, err)
	}
	life, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	read, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: title,
		Category: cat, Lifecycle: life, ReadState: &read, Tags: tags,
	})
	if err != nil {
		t.Fatalf("entry %d: %v", id, err)
	}
	return e
}

func fixture(t *testing.T) []domain.Entry {
	t.Helper()
	return []domain.Entry{
		mustEntry(t, 1, "Claude Code на автопилоте", "ai-agents", []string{"claude-code", "ci"}),
		mustEntry(t, 2, "Мы пытались заменить QA нейросетью", "testing", []string{"qa", "mcp"}),
		mustEntry(t, 3, "LLM, персональные данные и 152-ФЗ", "security", []string{"llm", "privacy"}),
	}
}

func ids(entries []domain.Entry) []int {
	out := make([]int, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID())
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilter(t *testing.T) {
	entries := fixture(t)
	for _, tc := range []struct {
		name, query string
		want        []int
	}{
		{"пустой запрос отдаёт всё", "", []int{1, 2, 3}},
		{"по заголовку", "автопилот", []int{1}},
		// Прежде здесь стояло «КЛАУДЕ» с ожиданием пустоты — то есть случай
		// проверял регистр отрицательным результатом, где ничего не найдено по
		// двум причинам сразу. С появлением транслитерации «клауде» находит
		// Claude, и это ровно то, ради чего слой заведён; регистр проверяется
		// запросом, который обязан найтись.
		{"регистр не важен", "АВТОПИЛОТЕ", []int{1}},
		{"латинский термин кириллицей", "клауде", []int{1}},
		{"по тегу", "mcp", []int{2}},
		{"по категории", "security", []int{3}},
		{"по id", "#3", []int{3}},
		{"пробелы обрезаются", "  qa  ", []int{2}},
		{"ничего не найдено", "квантовая физика", []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(tui.Filter(entries, tc.query))
			if !equal(got, tc.want) {
				t.Errorf("Filter(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// Несколько слов — это И, а не ИЛИ: сужать выдачу словами естественнее, чем
// расширять, и без этого второй термин бесполезен.
func TestFilter_allWordsMustMatch(t *testing.T) {
	entries := fixture(t)

	if got := ids(tui.Filter(entries, "claude ci")); !equal(got, []int{1}) {
		t.Errorf("got %v, want [1]", got)
	}
	if got := ids(tui.Filter(entries, "claude qa")); !equal(got, []int{}) {
		t.Errorf("got %v, want [] — слова из разных записей не должны совпасть", got)
	}
}

// withLifecycle rebuilds an entry in another lifecycle state — the constructor
// takes it up front, and the domain keeps entries immutable.
func withLifecycle(t *testing.T, e domain.Entry, state string) domain.Entry {
	t.Helper()
	life, err := domain.NewLifecycle(state)
	if err != nil {
		t.Fatalf("lifecycle %q: %v", state, err)
	}
	read, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	out, err := domain.NewEntry(domain.EntryParams{
		ID: e.ID(), Kind: e.Kind(), Title: e.Title(),
		Category: e.Category(), Lifecycle: life, ReadState: &read, Tags: e.Tags(),
	})
	if err != nil {
		t.Fatalf("rebuild entry: %v", err)
	}
	return out
}
