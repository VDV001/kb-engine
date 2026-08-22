package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Профилировщик — отдельная дверь, а не ещё один маршрут витрины.
//
// Причина не вкусовая. Витрину можно отдать наружу (`--addr` меняется), а
// pprof отдаёт дампы памяти и стеки всех горутин; повесив его на тот же
// mux, мы бы связали два решения, которые принимаются по разным причинам.
// Поэтому у него свой слушатель, и включается он собственным адресом.
func TestPprofHandler_servesProfiles(t *testing.T) {
	h := pprofHandler()
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap?debug=1"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: код %d, ждали 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: пустое тело", path)
		}
	}
}

// Отрицательный контроль к предыдущему: чужие пути профилировщик не обслуживает.
func TestPprofHandler_refusesOtherPaths(t *testing.T) {
	rec := httptest.NewRecorder()
	pprofHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("/api/stats на профилировщике ответил 200 — дверь шире, чем объявлена")
	}
}

// ⚠️ Главная проверка задачи: витрина не отдаёт профили НИ ПРИ КАКОМ раскладе.
//
// Смотреть на код ответа здесь нельзя — SPA-фолбэк отвечает 200 на любой путь,
// и ровно на этом три выпуска подряд «N маршрутов 200» ничего не доказывали.
// Различаем по ТЕЛУ: у страницы pprof есть слово «goroutine» и таблица профилей.
func TestServeHandler_neverServesPprof(t *testing.T) {
	dir := t.TempDir()
	catalog := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(catalog, []byte(`{"meta":{"categories":{}},"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := buildServeHandler(catalog, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	body := rec.Body.String()
	for _, marker := range []string{"goroutine", "Types of profiles available", "threadcreate"} {
		if strings.Contains(body, marker) {
			t.Fatalf("витрина отдала профили: в теле есть %q", marker)
		}
	}
}
