package ingest_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

// Категория, которой нет в словаре каталога, до сих пор проезжала обе двери:
// ни `add`, ни `inbox` не спрашивали meta.categories, и находил такую запись
// только аудит — потом, если кто-то его читал. Замер на живой базе: категория
// `testing` прожила пятнадцать дней одной записью, потому что сторож аудита
// запомнил находку при первом прогоне и молчал о ней как о старой.
//
// Проверка стоит в usecase, а не в каждой команде: планировщиков два, и правило,
// написанное дважды, разошлось бы — ровно как поиск, который жил копиями в
// терминале и вебе, пока #252 не свёл его в одно место.
func withLabels(t *testing.T, labels map[string]string, entries ...domain.Entry) *domain.Catalog {
	t.Helper()
	c, err := domain.NewCatalog(entries, domain.WithCategoryLabels(labels))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return c
}

func paramsInCategory(t *testing.T, category string) domain.EntryParams {
	t.Helper()
	p := artefactParams(t, "Группировка ошибок и анализ причин падений", "notes/rca.md")
	cat, err := domain.NewCategory(category)
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	p.Category = cat
	p.URL = "https://example.org/rca"
	return p
}

func TestPlanners_rejectACategoryOutsideTheDictionary(t *testing.T) {
	labels := map[string]string{
		"dev-practices": "Практики разработки: спецификации, коммиты",
		"golang":        "Go: язык, библиотеки, архитектура",
	}
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	plan := func(c *domain.Catalog, p domain.EntryParams) (ingest.Report, error) {
		_, rep, err := ingest.Plan(c, []domain.EntryParams{p}, now)
		return rep, err
	}
	planArtefacts := func(c *domain.Catalog, p domain.EntryParams) (ingest.Report, error) {
		_, rep, err := ingest.PlanArtefacts(c, []domain.EntryParams{p}, now)
		return rep, err
	}

	for _, planner := range []struct {
		name string
		run  func(*domain.Catalog, domain.EntryParams) (ingest.Report, error)
	}{
		{"Plan", plan},
		{"PlanArtefacts", planArtefacts},
	} {
		t.Run(planner.name+"/объявленная категория проходит", func(t *testing.T) {
			c := withLabels(t, labels)
			if _, err := planner.run(c, paramsInCategory(t, "dev-practices")); err != nil {
				t.Fatalf("объявленная категория должна проходить, получено: %v", err)
			}
		})

		t.Run(planner.name+"/незаявленная категория отвергается", func(t *testing.T) {
			c := withLabels(t, labels)
			_, err := planner.run(c, paramsInCategory(t, "testing"))
			if err == nil {
				t.Fatal("категория вне словаря должна быть отвергнута, ошибки нет")
			}
			if !errors.Is(err, ingest.ErrUndeclaredCategory) {
				t.Fatalf("ошибка должна распознаваться через errors.Is, получено: %v", err)
			}
			var undeclared *ingest.UndeclaredCategoryError
			if !errors.As(err, &undeclared) {
				t.Fatalf("ошибка должна нести подробности через errors.As, получено: %v", err)
			}
			if undeclared.Category != "testing" {
				t.Errorf("названа категория %q, ожидалась testing", undeclared.Category)
			}
			// Список объявленных нужен вызывающему, чтобы подсказать соседа:
			// «похоже на dev-practices». Собирать его второй раз в CLI значило бы
			// снова завести две копии одного знания.
			if len(undeclared.Declared) != len(labels) {
				t.Errorf("объявленных категорий названо %d, ожидалось %d", len(undeclared.Declared), len(labels))
			}
		})

		t.Run(planner.name+"/пустой словарь: не проверяем и говорим об этом", func(t *testing.T) {
			c := withLabels(t, nil)
			rep, err := planner.run(c, paramsInCategory(t, "testing"))
			if err != nil {
				t.Fatalf("без словаря проверять нечем — запись должна пройти, получено: %v", err)
			}
			if !rep.CategoriesUnchecked {
				t.Error("отчёт обязан назвать, что категория не проверялась: молчание тут неотличимо от проверки")
			}
		})

		t.Run(planner.name+"/со словарём отчёт не жалуется", func(t *testing.T) {
			c := withLabels(t, labels)
			rep, err := planner.run(c, paramsInCategory(t, "golang"))
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if rep.CategoriesUnchecked {
				t.Error("словарь есть — проверка состоялась, отчёт не должен говорить обратное")
			}
		})
	}
}
