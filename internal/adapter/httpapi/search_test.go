package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Поиска в HTTP API не было вовсе: фронт забирал /api/entries и фильтровал у
// себя подстрокой на TypeScript. Из-за этого поверхности разошлись измеримо —
// «кубернетес» давал 10 записей в терминале и ноль в браузере (#252).
//
// Эндпоинт нужен именно затем, чтобы у поиска остался ОДИН ответчик: тот же
// usecase, которым пользуется терминал.
func TestServer_search(t *testing.T) {
	srv := newTestServer()

	t.Run("находит по слову из заголовка", func(t *testing.T) {
		rec := get(t, srv, "/api/search?q=hello")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].ID != 1 {
			t.Fatalf("получено %d записей (%+v), ожидалась одна с id=1", len(got), got)
		}
	})

	t.Run("непопадание — пустой список, а не 404", func(t *testing.T) {
		rec := get(t, srv, "/api/search?q=телепортация")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got []json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("получено %d записей, ожидался пустой список", len(got))
		}
	})

	// Пустой запрос — это «покажи всё», а не ошибка: так ведёт себя терминал,
	// и расхождение поверхностей здесь было бы тем же дефектом в миниатюре.
	t.Run("пустой запрос отдаёт весь каталог", func(t *testing.T) {
		all := get(t, srv, "/api/entries")
		rec := get(t, srv, "/api/search?q=")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var a, b []json.RawMessage
		_ = json.Unmarshal(all.Body.Bytes(), &a)
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(a) != len(b) {
			t.Fatalf("пустой запрос вернул %d записей, а /api/entries — %d", len(b), len(a))
		}
	})
}
