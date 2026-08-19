package audit_test

import (
	"errors"
	"reflect"
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
// Множество разделов берётся ИЗ СТРУКТУРЫ, а не из списка, вписанного в тест.
// Прежняя редакция перечисляла четыре раздела из пяти, и `Canonical` можно было
// выключить целиком при зелёном наборе — тот самый класс «тест назван „каждый“,
// а проверяет подмножество» (issue #229). Теперь новое поле в Health валит тест
// до того, как окажется непроверенным.
func TestHealth_gathersEveryCheckTheScreenShows(t *testing.T) {
	svc := audit.NewService(fakeLoader{catalog: healthCatalog(t)})

	h, err := svc.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	// Каталог собран так, что каждая проверка находит ровно одну вещь: пустой
	// раздел здесь означает, что проверка не была вызвана, а не что база чиста.
	filled := map[string]func(audit.Health) bool{
		"Outdated":     func(h audit.Health) bool { return len(h.Outdated) > 0 },
		"Canonical":    func(h audit.Health) bool { return len(h.Canonical) > 0 },
		"Supersession": func(h audit.Health) bool { return len(h.Supersession) > 0 },
		"Duplicates":   func(h audit.Health) bool { return len(h.Duplicates) > 0 },
		"Links":        func(h audit.Health) bool { return h.Links.WithURL > 0 },
	}

	typ := reflect.TypeFor[audit.Health]()
	for field := range typ.Fields() {
		name := field.Name
		check, ok := filled[name]
		if !ok {
			t.Fatalf("раздел %s появился в Health, но его никто не проверяет — "+
				"допишите случай сюда, иначе проверку можно выключить молча", name)
		}
		if !check(h) {
			t.Errorf("раздел %s пуст — проверка не была вызвана", name)
		}
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
		// Кандидат в канон: на запись 9 ссылаются трое, и она не ссылается
		// обратно — взаимная ссылка опорой не считается.
		article(t, 6, articleParams{title: "Ссылается", lifecycle: "active", relatedIDs: []int{9}}),
		article(t, 7, articleParams{title: "Ссылается ещё", lifecycle: "active", relatedIDs: []int{9}}),
		article(t, 8, articleParams{title: "И третий", lifecycle: "active", relatedIDs: []int{9}}),
		article(t, 9, articleParams{title: "Опора", lifecycle: "active"}),
	)
}
