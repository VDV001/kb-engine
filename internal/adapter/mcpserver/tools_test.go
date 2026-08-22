package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// stubQuerier — каталог в памяти. Настоящий Service читает с диска, а вопрос
// этих тестов не в чтении, а в том, что MCP зовёт тот же поиск.
type stubQuerier struct {
	entries []domain.Entry
	err     error
}

func (s stubQuerier) Entries() ([]domain.Entry, error) { return s.entries, s.err }

func fixture() []domain.Entry {
	return []domain.Entry{
		{ID: 1, Title: "Kubernetes в проде", Category: "devops", Tags: []string{"k8s"}},
		{ID: 2, Title: "Разбор MCP", Category: "ai-agents-tools", Description: "протокол"},
		{ID: 3, Title: "Go: рантайм", Category: "golang"},
	}
}

// Главное требование задачи #273: MCP не заводит третью копию поиска, а зовёт
// тот же usecase. Проверяется контрастом — набор совпадает с search.FilterWith
// на тех же данных, а не «выглядит правдоподобным».
func TestSearchCatalog_sameSetAsUsecase(t *testing.T) {
	q := stubQuerier{entries: fixture()}
	for _, query := range []string{"kubernetes", "mcp", "go", "разбор"} {
		want := search.FilterWith(fixture(), query, search.Matcher{})
		got, err := mcpserver.SearchCatalog(q, search.Matcher{}, query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%q: MCP отдал %d записей, usecase — %d", query, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID {
				t.Fatalf("%q: позиция %d — MCP #%d, usecase #%d", query, i, got[i].ID, want[i].ID)
			}
		}
	}
}

// Отрицательный контроль из приёмки задачи: запрос, которого в базе нет,
// возвращает пусто, а не падение и не первую попавшуюся запись.
func TestSearchCatalog_unknownQueryReturnsEmpty(t *testing.T) {
	got, err := mcpserver.SearchCatalog(stubQuerier{entries: fixture()}, search.Matcher{}, "выдуманнаятемакоторойнет")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ожидалось пусто, отдано %d записей", len(got))
	}
}

// Выдуманный id — ошибка «не найдено», а не первая попавшаяся запись.
func TestGetEntry_unknownIDIsNotFound(t *testing.T) {
	_, err := mcpserver.GetEntry(stubQuerier{entries: fixture()}, 9999)
	if err == nil {
		t.Fatal("выдуманный id должен давать ошибку, а не запись")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("ошибка обязана называть искомый id, а сказано: %v", err)
	}
}

func TestGetEntry_returnsTheAskedEntry(t *testing.T) {
	got, err := mcpserver.GetEntry(stubQuerier{entries: fixture()}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 2 || got.Title != "Разбор MCP" {
		t.Fatalf("отдана не та запись: #%d %q", got.ID, got.Title)
	}
}
