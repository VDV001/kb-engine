package httpapi_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

// emptyQuery — каталог без записей: проверяем, что llms.txt остаётся валидным.
type emptyQuery struct{}

func (emptyQuery) Stats() (query.Stats, error)      { return query.Stats{}, nil }
func (emptyQuery) Entries() ([]domain.Entry, error) { return nil, nil }
func (emptyQuery) Health() (query.Health, error)    { return query.Health{}, nil }

// llms.txt — машиночитаемая карта базы для агентов и LLM-поисковиков (идея из
// Blume). Собирается ИЗ каталога: добавили категорию — она появилась без правки
// руками. Отдаётся только публичное, как и /kb/.
func TestLlmsTxt_builtFromCatalog(t *testing.T) {
	rec := get(t, newTestServer(), "/llms.txt")
	if rec.Code != 200 {
		t.Fatalf("код %d, ждали 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type %q, ждали text/plain", ct)
	}
	body := rec.Body.String()
	// Спецификация llms.txt: файл начинается с заголовка первого уровня.
	if !strings.HasPrefix(body, "# ") {
		t.Fatalf("файл не начинается с # заголовка: %.40q", body)
	}
	// Категория из каталога с её человеческой меткой, а не голым ключом.
	if !strings.Contains(body, "Go: язык и экосистема") {
		t.Fatalf("нет метки категории из каталога: %q", body)
	}
	// Ссылки на разделы витрины, чтобы агент знал, куда идти.
	if !strings.Contains(body, "/api/search") {
		t.Fatalf("нет ссылки на поиск: %q", body)
	}
}

// Отрицательный контроль: пустой каталог даёт валидный llms.txt с заголовком и
// пустым перечнем, а не 500. «Разделов нет» — законная форма, не ошибка.
func TestLlmsTxt_emptyCatalogStillValid(t *testing.T) {
	srv := httpapi.NewServer(emptyQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{CurrentVersion: "0.9.0"}, nil },
		httpapi.Documents{}, testEngine, nil)
	rec := get(t, srv, "/llms.txt")
	if rec.Code != 200 {
		t.Fatalf("пустой каталог: код %d, ждали 200", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "# ") {
		t.Fatalf("нет заголовка на пустом каталоге: %q", rec.Body.String())
	}
}
