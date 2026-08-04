package freshness_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/freshness"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Страница «что в работе сейчас» тухнет тихо: текст остаётся правдоподобным, а
// мир вокруг уходит вперёд. Владелец увидел это на своём же примере — в шапке
// стояло «четыре PR смержены», когда их было восемь.
//
// Признак протухания — не возраст файла. Страница, которую не трогали месяц, но
// и база вокруг которой не менялась, верна. Отставание — это события ПОСЛЕ
// последней правки: новые записи, новая версия базы, новые операции.
func TestCheck_namesWhatHappenedAfterTheLastEdit(t *testing.T) {
	got := freshness.Check(freshness.Input{
		Now:      day(2026, 8, 10),
		EditedAt: day(2026, 8, 4),
		Entries: []freshness.EntryFact{
			{ID: 1439, Title: "Запись после правки", Added: day(2026, 8, 6)},
			{ID: 1440, Title: "Ещё одна", Added: day(2026, 8, 7)},
			{ID: 1200, Title: "Старая, до правки", Added: day(2026, 7, 1)},
		},
		Version:     "0.30.0",
		VersionDate: day(2026, 8, 8),
		Operations:  []time.Time{day(2026, 8, 5), day(2026, 8, 9), day(2026, 7, 2)},
	})

	if !got.Behind {
		t.Fatal("отставание не замечено")
	}
	// Каждая опора названа отдельно: «страница устарела» без причины нечем
	// закрыть, а список фактов сам подсказывает, что дописать.
	kinds := map[string]freshness.Fact{}
	for _, f := range got.Facts {
		kinds[f.Kind] = f
	}
	if f, ok := kinds["catalog"]; !ok || f.Count != 2 {
		t.Errorf("записи после правки = %+v, ожидалось 2", f)
	}
	if f, ok := kinds["version"]; !ok || !strings.Contains(f.Text, "0.30.0") {
		t.Errorf("новая версия базы не названа: %+v", f)
	}
	if f, ok := kinds["ledger"]; !ok || f.Count != 2 {
		t.Errorf("операции после правки = %+v, ожидалось 2", f)
	}
}

// Ничего не случилось — страница свежая, и молчание здесь содержательно:
// предупреждение, которое горит всегда, перестают читать.
func TestCheck_staysQuietWhenNothingHappened(t *testing.T) {
	got := freshness.Check(freshness.Input{
		Now:      day(2026, 9, 10),
		EditedAt: day(2026, 8, 4),
		Entries:  []freshness.EntryFact{{ID: 1, Added: day(2026, 7, 1)}},
		Version:  "0.29.1", VersionDate: day(2026, 8, 4),
		Operations: []time.Time{day(2026, 8, 3)},
	})

	if got.Behind {
		t.Errorf("свежая страница помечена отставшей: %+v", got.Facts)
	}
	if got.Draft != "" {
		t.Error("черновик предложен там, где дописывать нечего")
	}
}

// Черновик — заготовка, а не запись за человека: движок собирает факты, слова
// остаются его. Поэтому это markdown, который копируют, а не файл, который
// движок правит.
func TestCheck_buildsADraftBlockFromTheFacts(t *testing.T) {
	got := freshness.Check(freshness.Input{
		Now:      day(2026, 8, 10),
		EditedAt: day(2026, 8, 4),
		Entries: []freshness.EntryFact{
			{ID: 1439, Title: "Шпаргалка по kbengine", Added: day(2026, 8, 6)},
		},
		Version: "0.30.0", VersionDate: day(2026, 8, 8),
	})

	for _, want := range []string{"## 2026-08-10", "#1439", "Шпаргалка по kbengine", "0.30.0"} {
		if !strings.Contains(got.Draft, want) {
			t.Errorf("в черновике нет %q:\n%s", want, got.Draft)
		}
	}
}

// Дата правки неизвестна — это «не знаю», а не «всё хорошо». Инструмент обязан
// называть, чего он не проверил: иначе страница выглядит проверенной, будучи
// непроверенной.
func TestCheck_saysWhenItCannotTell(t *testing.T) {
	got := freshness.Check(freshness.Input{
		Now:     day(2026, 8, 10),
		Entries: []freshness.EntryFact{{ID: 1, Added: day(2026, 8, 9)}},
	})

	if got.Behind {
		t.Error("объявил отставание, не зная даты правки")
	}
	if !got.Unknown {
		t.Error("не признался, что дату правки не знает")
	}
}

// Список записей не растёт бесконечно: страница показывает несколько первых и
// говорит, сколько всего. Двадцать заголовков подряд — это не подсказка, а
// вторая лента поверх той, что уже есть на Dashboard.
func TestCheck_limitsHowManyEntriesItNames(t *testing.T) {
	var entries []freshness.EntryFact
	for i := range 12 {
		entries = append(entries, freshness.EntryFact{ID: 1500 + i, Title: "Запись", Added: day(2026, 8, 6)})
	}

	got := freshness.Check(freshness.Input{
		Now: day(2026, 8, 10), EditedAt: day(2026, 8, 4), Entries: entries,
	})

	for _, f := range got.Facts {
		if f.Kind != "catalog" {
			continue
		}
		if f.Count != 12 {
			t.Errorf("счёт = %d, ожидалось 12", f.Count)
		}
		if len(f.IDs) > 5 {
			t.Errorf("названо %d записей, ожидалось не больше пяти", len(f.IDs))
		}
	}
}
