package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/query"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// stubQuerier — каталог в памяти. Настоящий Service читает с диска, а вопрос
// этих тестов не в чтении, а в том, что MCP зовёт тот же поиск.
type stubQuerier struct {
	entries []domain.Entry
	err     error
}

func (s stubQuerier) Entries() ([]domain.Entry, error) { return s.entries, s.err }

func (s stubQuerier) Stats() (query.Stats, error) {
	return query.Stats{Total: len(s.entries)}, s.err
}

// entry строит запись через конструктор домена: прямое &domain.Entry{...} вне
// пакета domain запрещено проектом, и здесь это не формальность — поля закрыты,
// инварианты проверяет NewEntry.
func entry(t *testing.T, id int, title, desc string, tags ...string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("articles")
	if err != nil {
		t.Fatalf("category: %v", err)
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
		ID: id, Kind: domain.KindArticle, Title: title, Description: desc,
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
		entry(t, 1, "Оркестрация контейнеров", "", "kubernetes"),
		entry(t, 2, "Разбор MCP", "протокол для агентов"),
		entry(t, 3, "Go: рантайм", ""),
	}
}

// Главное требование задачи #273: MCP не заводит третью копию поиска, а зовёт
// тот же usecase. Проверяется контрастом — набор совпадает с search.FilterWith
// на тех же данных, а не «выглядит правдоподобным».
func TestSearchCatalog_sameSetAsUsecase(t *testing.T) {
	q := stubQuerier{entries: fixture(t)}
	for _, query := range []string{"kubernetes", "кубернетес", "mcp", "разбор", "протокол"} {
		want := search.FilterWith(fixture(t), query, search.Matcher{})
		got, err := mcpserver.SearchCatalog(q, search.Matcher{}, query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%q: MCP отдал %d записей, usecase — %d", query, len(got), len(want))
		}
		for i := range want {
			if got[i].ID() != want[i].ID() {
				t.Fatalf("%q: позиция %d — MCP #%d, usecase #%d", query, i, got[i].ID(), want[i].ID())
			}
		}
	}
}

// Отрицательный контроль из приёмки задачи: запрос, которого в базе нет,
// возвращает пусто, а не падение и не первую попавшуюся запись.
func TestSearchCatalog_unknownQueryReturnsEmpty(t *testing.T) {
	got, err := mcpserver.SearchCatalog(stubQuerier{entries: fixture(t)}, search.Matcher{}, "выдуманнаятемакоторойнет")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ожидалось пусто, отдано %d записей", len(got))
	}
}

// Выдуманный id — ошибка «не найдено», а не первая попавшаяся запись.
func TestGetEntry_unknownIDIsNotFound(t *testing.T) {
	_, err := mcpserver.GetEntry(stubQuerier{entries: fixture(t)}, 9999)
	if err == nil {
		t.Fatal("выдуманный id должен давать ошибку, а не запись")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("ошибка обязана называть искомый id, а сказано: %v", err)
	}
}

func TestGetEntry_returnsTheAskedEntry(t *testing.T) {
	got, err := mcpserver.GetEntry(stubQuerier{entries: fixture(t)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != 2 || got.Title() != "Разбор MCP" {
		t.Fatalf("отдана не та запись: #%d %q", got.ID(), got.Title())
	}
}
