package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

// Метрика говорит «медленно», но не говорит ГДЕ: чтение каталога, сводка или
// кодирование ответа. Server-Timing отвечает на это без единой новой
// зависимости — заголовок стандартный, и браузер показывает разбивку прямо во
// вкладке Network.
//
// Выключено по умолчанию: заголовок раскрывает внутреннее устройство запроса,
// а витрину можно отдать наружу. Включается тем же способом, что и остальное, —
// решением человека, а не наследством.
func TestServerTiming_offByDefault(t *testing.T) {
	rec := get(t, newTestServer(), "/api/stats")
	if h := rec.Header().Get("Server-Timing"); h != "" {
		t.Fatalf("заголовок пришёл без флага: %q", h)
	}
}

func TestServerTiming_namesTheStepsWhenOn(t *testing.T) {
	rec := get(t, newTestServerWith(httpapi.WithServerTiming()), "/api/stats")
	h := rec.Header().Get("Server-Timing")
	if h == "" {
		t.Fatal("флаг включён, а заголовка нет")
	}
	// total обязателен: без него шаги не с чем сравнивать, и «где медленно»
	// снова становится вопросом без ответа.
	if !strings.Contains(h, "total;dur=") {
		t.Fatalf("нет total: %q", h)
	}
	// Хотя бы один ИМЕНОВАННЫЙ шаг: заголовок с одним total ничем не лучше
	// того же числа в метрике.
	if !strings.Contains(h, "catalog;dur=") {
		t.Fatalf("нет шага catalog: %q", h)
	}
}

// Отрицательный контроль: маршрут, который не читает каталог, не должен
// сообщать о шаге, которого не делал. Иначе заголовок описывал бы не запрос,
// а список всех шагов, какие бывают.
func TestServerTiming_reportsOnlyStepsThatRan(t *testing.T) {
	rec := get(t, newTestServerWith(httpapi.WithServerTiming()), "/api/engine")
	h := rec.Header().Get("Server-Timing")
	if strings.Contains(h, "catalog;dur=") {
		t.Fatalf("/api/engine каталог не читает, а шаг заявлен: %q", h)
	}
	if !strings.Contains(h, "total;dur=") {
		t.Fatalf("total должен быть у любого ответа: %q", h)
	}
}

// /metrics обслуживается мимо инструментовки, и заголовка там быть не должно:
// его читает Prometheus, а не человек.
func TestServerTiming_notOnMetrics(t *testing.T) {
	rec := get(t, newTestServerWith(httpapi.WithServerTiming()), "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics: код %d", rec.Code)
	}
	if h := rec.Header().Get("Server-Timing"); h != "" {
		t.Fatalf("на /metrics пришёл заголовок: %q", h)
	}
}
