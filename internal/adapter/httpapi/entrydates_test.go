package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Две даты, которые база хранила и не показывала.
//
// `habr_date` — когда материал вышел у автора, `deep_read_date` — когда его
// разобрали здесь. Ни одна не выводится ни на один экран, хотя первая отвечает
// на вопрос «насколько это свежее», а вторая отличает прочитанное бегло от
// разобранного всерьёз.
//
// Сводить их с `source_date` было нельзя, и это проверялось замером, а не
// рассуждением: `source_date` одна на всю партию импорта (внутри партии 20 —
// ровно одно значение на все записи), а `habr_date` у каждой статьи своя.
// Разные вопросы, разные поля.
func TestServer_entries_carryHabrAndDeepReadDates(t *testing.T) {
	rec := get(t, newTestServer(), "/api/entries")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body []struct {
		ID           int    `json:"id"`
		HabrDate     string `json:"habr_date"`
		DeepReadDate string `json:"deep_read_date"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(body) == 0 {
		t.Fatal("записей нет")
	}
	var withHabr, withDeep int
	for _, e := range body {
		if e.HabrDate != "" {
			withHabr++
		}
		if e.DeepReadDate != "" {
			withDeep++
		}
	}
	if withHabr == 0 {
		t.Error("дата публикации у автора не доехала до API")
	}
	if withDeep == 0 {
		t.Error("дата глубокого разбора не доехала до API")
	}
}

// Обе даты необязательны и у большинства записей отсутствуют: habr_date есть у
// 55 записей живой базы из 1395, deep_read_date у 15. Пустая строка в JSON
// заставила бы вид отличать «даты нет» от «дата пустая» — поэтому поля
// опускаются целиком, как и остальные необязательные.
func TestServer_entries_datesAreOmittedWhenAbsent(t *testing.T) {
	rec := get(t, newTestServer(), "/api/entries")
	var raw []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range raw {
		if v, ok := e["habr_date"]; ok && v == "" {
			t.Errorf("запись %v несёт пустой habr_date вместо отсутствия поля", e["id"])
		}
		if v, ok := e["deep_read_date"]; ok && v == "" {
			t.Errorf("запись %v несёт пустой deep_read_date вместо отсутствия поля", e["id"])
		}
	}
}
