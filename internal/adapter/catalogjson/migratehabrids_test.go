package catalogjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Номер статьи есть в адресе у 1266 записей каталога, а поле habr_id заполнено
// у 506 — то есть у большинства он известен и потерян. Пока это так, дедуп по
// полю смотрит на часть базы и молчит про остальное.
func TestMigrateHabrIDs(t *testing.T) {
	path := catalogWith(t, `[
	  {"id": 1, "title": "Обычная статья", "url": "https://habr.com/ru/articles/1065834/"},
	  {"id": 2, "title": "Корпоративный блог", "url": "https://habr.com/ru/companies/otus/articles/1022618/"},
	  {"id": 3, "title": "Уже проставлен", "url": "https://habr.com/ru/articles/999/", "habr_id": 999},
	  {"id": 4, "title": "Чужой сайт", "url": "https://example.com/articles/123/"},
	  {"id": 5, "title": "Без адреса"},
	  {"id": 6, "title": "Расходится с адресом", "url": "https://habr.com/ru/articles/777/", "habr_id": 555}
	]`)

	plan, err := catalogjson.MigrateHabrIDs(path, false)
	if err != nil {
		t.Fatalf("MigrateHabrIDs (план): %v", err)
	}
	// Заполняются только пустые: запись 3 уже верна, 4 и 5 номера не несут.
	if len(plan.Filled) != 2 {
		t.Fatalf("к заполнению %d, ожидалось 2: %+v", len(plan.Filled), plan.Filled)
	}
	// Расхождение — не наше дело: движок не знает, адрес неверен или поле, и
	// молча выбрать одно значит стереть чужое решение.
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].EntryID != 6 {
		t.Fatalf("расхождения не названы: %+v", plan.Conflicts)
	}
	// План ничего не пишет.
	if got := habrIDOf(t, path, 1); got != 0 {
		t.Errorf("план записал habr_id=%d, файл должен быть нетронут", got)
	}

	if _, err := catalogjson.MigrateHabrIDs(path, true); err != nil {
		t.Fatalf("MigrateHabrIDs (запись): %v", err)
	}
	for id, want := range map[int]int{1: 1065834, 2: 1022618, 3: 999, 6: 555} {
		if got := habrIDOf(t, path, id); got != want {
			t.Errorf("#%d: habr_id = %d, ожидалось %d", id, got, want)
		}
	}
	if got := habrIDOf(t, path, 4); got != 0 {
		t.Errorf("чужому источнику выдуман habr_id = %d", got)
	}
}

func catalogWith(t *testing.T, entries string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	doc := `{"meta": {"description": "тест"}, "entries": ` + entries + `}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func habrIDOf(t *testing.T, path string, id int) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Entries []struct {
			ID     int  `json:"id"`
			HabrID *int `json:"habr_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range doc.Entries {
		if e.ID == id && e.HabrID != nil {
			return *e.HabrID
		}
	}
	return 0
}
