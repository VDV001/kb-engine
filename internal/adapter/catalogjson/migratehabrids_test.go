package catalogjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	  {"id": 6, "title": "Расходится с адресом", "url": "https://habr.com/ru/articles/777/", "habr_id": 555},
	  {"id": 7, "title": "Номер строкой", "url": "https://habr.com/ru/articles/1030896/", "habr_id": "1030896"},
	  {"id": 8, "title": "Строка и расхождение", "url": "https://habr.com/ru/articles/888/", "habr_id": "444"}
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
	if len(plan.Conflicts) != 2 {
		t.Fatalf("расхождений %d, ожидалось 2 (#6 и #8): %+v", len(plan.Conflicts), plan.Conflicts)
	}
	// Поле хранит номер то числом, то строкой: на живом каталоге 281 против 225.
	// Пока это так, сравнение с числом промахивается на половине записей, и
	// промах выглядит как «такой статьи в базе нет».
	if len(plan.Normalized) != 1 || plan.Normalized[0].EntryID != 7 {
		t.Fatalf("строковый номер не приведён к числу: %+v", plan.Normalized)
	}
	// План ничего не пишет.
	if raw, _ := habrIDRaw(t, path, 1); raw != "" {
		t.Errorf("план записал habr_id=%s, файл должен быть нетронут", raw)
	}

	if _, err := catalogjson.MigrateHabrIDs(path, true); err != nil {
		t.Fatalf("MigrateHabrIDs (запись): %v", err)
	}
	for id, want := range map[int]int{1: 1065834, 2: 1022618, 3: 999, 6: 555, 7: 1030896} {
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
	raw, num := habrIDRaw(t, path, id)
	if raw == "" {
		return 0
	}
	if !num {
		t.Fatalf("#%d: habr_id остался не числом: %s", id, raw)
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("#%d: habr_id %q не число: %v", id, raw, err)
	}
	return v
}

// habrIDRaw возвращает поле как есть и признак «это число».
//
// Сырым, потому что каталог держит номер и числом, и строкой: разбор сразу в
// int падает на живых данных, и падал бы в этом тесте, скрывая то, ради чего он
// написан.
func habrIDRaw(t *testing.T, path string, id int) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Entries []struct {
			ID     int             `json:"id"`
			HabrID json.RawMessage `json:"habr_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range doc.Entries {
		if e.ID != id || len(e.HabrID) == 0 {
			continue
		}
		v := string(e.HabrID)
		if v == "null" {
			return "", false
		}
		if v[0] == '"' {
			return strings.Trim(v, `"`), false
		}
		return v, true
	}
	return "", false
}
