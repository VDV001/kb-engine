package audit_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// Health собирает в одном месте ответ на вопрос «что с базой не так». До этого
// состав здоровья был описан дважды: в HTTP-обработчиках, каждый из которых
// зовёт свою проверку, и в самой странице, которая складывает их ответы. Третья
// копия — в терминале — разошлась бы с обеими, и разошлась бы молча: экран,
// показывающий на одну проверку меньше, выглядит ровно так же, как экран,
// на котором эта проверка ничего не нашла.
func TestHealth_gathersEveryCheckTheScreenShows(t *testing.T) {
	svc := audit.NewService(fakeLoader{catalog: healthCatalog(t)})

	h, err := svc.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	// Каталог собран так, что каждая проверка находит ровно одну вещь: пустой
	// раздел здесь означает, что проверка не была вызвана, а не что база чиста.
	if len(h.Outdated) == 0 {
		t.Error("нет кандидатов в устаревшие")
	}
	if len(h.Supersession) == 0 {
		t.Error("нет находок по замещению")
	}
	if len(h.Duplicates) == 0 {
		t.Error("нет групп дублей")
	}
	if h.Links.WithURL == 0 {
		t.Error("здоровье ссылок не посчитано")
	}
}

// Отказ загрузки — это отказ всей сводки, а не пустое здоровье. Экран, который
// показывает ноль находок вместо «каталог не прочитан», говорит, что база
// в порядке, ровно тогда, когда о ней ничего не известно.
func TestHealth_loaderError(t *testing.T) {
	sentinel := errors.New("boom")
	svc := audit.NewService(fakeLoader{err: sentinel})

	if _, err := svc.Health(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, ожидался sentinel", err)
	}
}

// healthCatalog — каталог, в котором каждая проверка находит по одной вещи.
func healthCatalog(t *testing.T) *domain.Catalog {
	t.Helper()
	code := 404
	missing := 999
	return catalogOf(t,
		// Дубль по адресу: две записи с одним url.
		article(t, 1, articleParams{title: "Дубль", lifecycle: "active", url: "https://example.com/a"}),
		article(t, 2, articleParams{title: "Дубль", lifecycle: "active", url: "https://example.com/a"}),
		// Кандидат в устаревшие: слово-признак в заголовке.
		article(t, 3, articleParams{title: "Материал снят", lifecycle: "active"}),
		// Замещение в никуда: ссылается на запись, которой в каталоге нет.
		article(t, 4, articleParams{title: "Замена", lifecycle: "active", supersedesID: &missing}),
		// Мёртвая ссылка — попадает в здоровье ссылок.
		healthEntry(t, 5, "https://example.com/gone", "2026-08-01", &code),
	)
}
