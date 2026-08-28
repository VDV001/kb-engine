package runs_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// Список вызовов — это ответ на вопрос «о чём агент спрашивал базу», и он
// отвечает на него текстом запроса. Свод по инструментам (Build) отвечает на
// другой вопрос — «сколько раз», — и одним списком их не заменить.
func TestCalls_returnsWhatWasAskedNewestFirst(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	recs := []domain.RunRecord{
		at(t, runs.ToolCommand("search_catalog"), now.Add(-3*time.Hour), 0, "ddd"),
		at(t, "fin", now.Add(-2*time.Hour), 0, "add", "--amount", "418.50", "--place", "Такси"),
		at(t, runs.ToolCommand("get_entry"), now.Add(-time.Hour), 1, "#9999"),
		at(t, runs.ToolCommand("stats"), now.Add(-30*time.Minute), 0),
	}

	got, err := runs.Calls(journalStub{recs: recs, exists: true}, 10)
	if err != nil {
		t.Fatalf("Calls: %v", err)
	}

	// Три вызова из четырёх записей: команда движка вызовом не является.
	if len(got) != 3 {
		t.Fatalf("вызовов %d, ждали 3: %+v", len(got), got)
	}
	// Новейшие сверху: журнал читают, чтобы увидеть последнее, а не первое.
	if got[0].Tool != "stats" || got[2].Tool != "search_catalog" {
		t.Errorf("порядок неверный: %+v", got)
	}
	if got[2].Query != "ddd" {
		t.Errorf("запрос поиска = %q, ждали ddd", got[2].Query)
	}
	// Отказ виден: «спросили и не получили ответа» — то, ради чего журнал и
	// заводился, и в списке это должно отличаться от удачного вызова.
	if got[1].OK {
		t.Errorf("промах по id показан удачным вызовом: %+v", got[1])
	}
	// У сводки спрашивать нечего — пустой запрос, а не выдуманный.
	if got[0].Query != "" {
		t.Errorf("у stats появился запрос %q", got[0].Query)
	}
	// ⚠️ Главная проверка: аргументы КОМАНД движка сюда не попадают ни при
	// каких условиях — в них настоящие суммы и места владельца.
	for _, c := range got {
		if c.Query == "418.50" || c.Query == "Такси" || c.Tool == "fin" {
			t.Fatalf("в список вызовов утёк аргумент команды движка: %+v", c)
		}
	}
}

// Предел нужен странице: журнал растёт вечно, а показывают последнее.
func TestCalls_limitTakesTheNewest(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	var recs []domain.RunRecord
	for i := range 5 {
		recs = append(recs, at(t, runs.ToolCommand("search_catalog"),
			now.Add(-time.Duration(i)*time.Hour), 0, string(rune('а'+i))))
	}
	got, err := runs.Calls(journalStub{recs: recs, exists: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Query != "а" {
		t.Fatalf("предел взял не новейшие: %+v", got)
	}
}

// Журнала нет вовсе — законный ответ, а не ошибка: движок мог ни разу его не
// писать. Пустой список и отказ здесь означают разное.
func TestCalls_missingJournalIsEmptyNotError(t *testing.T) {
	got, err := runs.Calls(journalStub{exists: false}, 10)
	if err != nil {
		t.Fatalf("отсутствие журнала не ошибка: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("вызовов %d, ждали 0", len(got))
	}
}
