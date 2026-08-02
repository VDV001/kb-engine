package audit_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// healthEntry — статья с адресом, датой проверки и кодом ответа. Код нужен
// именно указателем: его отсутствие при наличии даты означает 200, а не ноль.
func healthEntry(t *testing.T, id int, url, checked string, code *int) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("ai-agents-tools")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: "T", Category: cat,
		Lifecycle: lc, ReadState: &rs, URL: url,
		DriftCheckDate: parseDay(t, checked),
		DriftHTTPCode:  code,
	})
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

func httpCode(n int) *int { return &n }

// Сводка отвечает на вопрос, который база держала при себе: что она узнала про
// собственные ссылки. Скан пишет результат в каталог с 01.08, но ни один экран
// его не показывал — сотня отказов 403 существовала только внутри файла.
//
// Отсутствие кода при наличии даты означает 200: скан записывает код только
// когда ответ был не 200 (см. ApplyDrift). Поэтому живые считаются вычитанием,
// а не собственным полем — и это ровно та деталь, которую легко переврать.
func TestService_LinkHealth(t *testing.T) {
	c := catalogOf(t,
		healthEntry(t, 1, "https://h/1", "2026-08-01", nil),           // 200
		healthEntry(t, 2, "https://h/2", "2026-08-01", nil),           // 200
		healthEntry(t, 3, "https://h/3", "2026-08-01", httpCode(302)), // переехала
		healthEntry(t, 4, "https://h/4", "2026-08-01", httpCode(404)), // удалена
		healthEntry(t, 5, "https://h/5", "2026-08-01", httpCode(403)), // не знаем
		healthEntry(t, 6, "https://h/6", "", nil),                     // не спрашивали
		healthEntry(t, 7, "", "", nil),                                // свой артефакт
	)

	got, err := audit.NewService(fakeLoader{catalog: c}).LinkHealth()
	if err != nil {
		t.Fatalf("LinkHealth: %v", err)
	}

	want := audit.LinkHealth{
		Alive: 2, Moved: 1, Gone: 1, Undecidable: 1, Unchecked: 1, WithURL: 6,
	}
	if got != want {
		t.Errorf("LinkHealth = %+v, want %+v", got, want)
	}
}

// Запись без адреса не участвует ни в одной доле: делить на неё значит занижать
// здоровье базы за счёт собственных стандартов и разборов, у которых url нет и
// не должно быть.
func TestService_LinkHealth_entriesWithoutURLAreNotCounted(t *testing.T) {
	c := catalogOf(t,
		healthEntry(t, 1, "", "", nil),
		healthEntry(t, 2, "", "", nil),
	)
	got, err := audit.NewService(fakeLoader{catalog: c}).LinkHealth()
	if err != nil {
		t.Fatalf("LinkHealth: %v", err)
	}
	if got.WithURL != 0 || got.Unchecked != 0 {
		t.Errorf("LinkHealth = %+v, want нули: записей с адресом нет", got)
	}
}

// 403 не приписывается ни к живым, ни к мёртвым — habr отвечает им и на снятую
// статью, и на бота, которого не стал обслуживать. Это состояние «база не
// знает», и оно обязано быть видно отдельным числом, а не растворяться.
func TestService_LinkHealth_403IsItsOwnState(t *testing.T) {
	c := catalogOf(t,
		healthEntry(t, 1, "https://h/1", "2026-08-01", httpCode(403)),
		healthEntry(t, 2, "https://h/2", "2026-08-01", httpCode(451)),
	)
	got, err := audit.NewService(fakeLoader{catalog: c}).LinkHealth()
	if err != nil {
		t.Fatalf("LinkHealth: %v", err)
	}
	if got.Undecidable != 2 || got.Alive != 0 || got.Gone != 0 {
		t.Errorf("LinkHealth = %+v, want оба в Undecidable", got)
	}
}
