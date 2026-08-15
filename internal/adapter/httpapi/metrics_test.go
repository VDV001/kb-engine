package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// Метрики отдаются в текстовом формате Prometheus. Формат — обычный текст, и
// именно поэтому здесь нет клиентской библиотеки: она удвоила бы четыре прямые
// зависимости движка ради того, что печатается через fmt.
func TestServer_metrics(t *testing.T) {
	rec := get(t, newTestServer(), "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Content-Type версионирован намеренно: без версии Prometheus разбирает
	// ответ по умолчанию, и смена формата однажды пройдёт незамеченной.
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain…", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		// Версия сборки: без неё по метрике не понять, какой именно бинарь её отдал.
		`kbengine_build_info{version="0.9.9",commit="abc1234"} 1`,
		// Размер каталога — то, ради чего эти метрики вообще заводятся: он
		// растёт, и однажды упрётся в потолок хранилища.
		"kbengine_catalog_entries 2",
		// Каждая метрика обязана нести HELP и TYPE: без них график подписан
		// именем переменной, и через месяц никто не помнит, что это значит.
		"# HELP kbengine_catalog_entries",
		"# TYPE kbengine_catalog_entries gauge",
		"# TYPE kbengine_http_requests_total counter",
		"# TYPE kbengine_http_request_duration_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("в ответе нет %q\nполучено:\n%s", want, body)
		}
	}
}

// Счётчик обязан считать. Проверка не «метрика присутствует», а «её значение
// меняется от работы сервера» — присутствующая, но всегда нулевая метрика
// выглядит на графике точно так же, как исправная в тихий час.
func TestServer_metricsCountRequests(t *testing.T) {
	srv := newTestServer()

	get(t, srv, "/api/stats")
	get(t, srv, "/api/stats")
	get(t, srv, "/healthz")

	body := get(t, srv, "/metrics").Body.String()
	for _, want := range []string{
		`kbengine_http_requests_total{route="/api/stats",code="200"} 2`,
		`kbengine_http_requests_total{route="/healthz",code="200"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("в ответе нет %q\nполучено:\n%s", want, body)
		}
	}
}

// Маршрут в метке — шаблон, а не запрошенный путь. Иначе каждый /api/maps/{id}
// заводит собственную серию, и через сотню карт Prometheus хранит сотню рядов
// вместо одного: тот самый взрыв кардинальности, которым метрики убивают базу.
func TestServer_metricsRouteLabelIsThePattern(t *testing.T) {
	srv := newTestServer()

	get(t, srv, "/api/maps/engine")
	get(t, srv, "/api/maps/cowork")

	body := get(t, srv, "/metrics").Body.String()
	if strings.Contains(body, `route="/api/maps/engine"`) {
		t.Error("в метке маршрута оказался конкретный путь — серия на каждую карту")
	}
	if !strings.Contains(body, `route="/api/maps/{id}"`) {
		t.Errorf("нет серии по шаблону маршрута\nполучено:\n%s", body)
	}
}

// Сам /metrics в счётчик не входит: опрос идёт каждые несколько секунд и был бы
// самой частой строкой в собственном отчёте, перекрывая настоящий трафик.
func TestServer_metricsExcludesItself(t *testing.T) {
	srv := newTestServer()

	get(t, srv, "/metrics")
	body := get(t, srv, "/metrics").Body.String()

	if strings.Contains(body, `route="/metrics"`) {
		t.Errorf("эндпоинт метрик считает сам себя\nполучено:\n%s", body)
	}
}
