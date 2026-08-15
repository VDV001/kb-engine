package httpapi

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Формат экспозиции Prometheus — это текст: имя, метки, число, перевод строки.
// Клиентская библиотека здесь удвоила бы четыре прямые зависимости движка ради
// того, что печатается через fmt, поэтому её нет.
//
// ponytail: собственная реализация покрывает счётчик и гистограмму с
// фиксированными корзинами — ровно то, что нужно для RED (частота, ошибки,
// длительность). Потолок: нет exemplars, нет OpenMetrics, нет метрик рантайма
// Go. Когда понадобится хоть одно — брать client_golang, а не дописывать это.

// bucketsSeconds — границы корзин длительности. Взяты вокруг того, что движок
// реально делает: чтение каталога с диска занимает десятки миллисекунд, а всё,
// что дольше секунды, уже повод смотреть отдельно.
var bucketsSeconds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type requestKey struct {
	route string
	code  int
}

type histogram struct {
	// Слайс, а не массив: длина берётся из bucketsSeconds, а границы корзин —
	// значение, которое правят, а не константа языка.
	counts []uint64
	sum    float64
	count  uint64
}

// metrics хранит счётчики процесса. Живёт столько же, сколько сервер: рестарт
// обнуляет ряды, и это нормально — Prometheus знает про сброс счётчика.
type metrics struct {
	mu     sync.Mutex
	totals map[requestKey]uint64
	hist   map[string]*histogram
}

func newMetrics() *metrics {
	return &metrics{
		totals: make(map[requestKey]uint64),
		hist:   make(map[string]*histogram),
	}
}

func (m *metrics) observe(route string, code int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totals[requestKey{route: route, code: code}]++

	h := m.hist[route]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(bucketsSeconds))}
		m.hist[route] = h
	}
	secs := d.Seconds()
	h.sum += secs
	h.count++
	for i, b := range bucketsSeconds {
		if secs <= b {
			h.counts[i]++
		}
	}
}

// statusRecorder запоминает код ответа: http.ResponseWriter его не отдаёт, а
// без кода счётчик не отличит успешные запросы от пятисоток — то есть букву E
// в RED.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	// Обработчик, написавший тело без WriteHeader, ответил 200 — иначе код
	// остался бы нулём и попал в метку как "0".
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// instrument оборачивает mux, считая запросы по ШАБЛОНУ маршрута, а не по
// запрошенному пути. Разница решающая: путей вида /api/maps/<id> столько же,
// сколько карт, и каждый завёл бы собственный временной ряд. Это взрыв
// кардинальности — то, чем метрики убивают хранилище.
//
// Шаблон берётся из http.Request.Pattern, который ServeMux заполняет сам после
// сопоставления; собственная таблица маршрутов разошлась бы с регистрацией на
// первом же новом эндпоинте.
func instrument(mux *http.ServeMux, m *metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == metricsPath {
			mux.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		mux.ServeHTTP(rec, r)
		if rec.code == 0 {
			rec.code = http.StatusOK
		}
		m.observe(routeOf(r), rec.code, time.Since(start))
	})
}

const metricsPath = "/metrics"

// routeOf возвращает шаблон, по которому запрос попал в обработчик. Пустой
// шаблон означает, что не подошёл ни один — такие запросы сводятся в одну
// серию, иначе сканер чужих адресов насоздаёт рядов сколько захочет.
func routeOf(r *http.Request) string {
	pattern := r.Pattern
	if pattern == "" {
		return "unmatched"
	}
	// Шаблон приходит как "GET /api/stats"; метод в метке не нужен, у движка
	// все маршруты кроме одного — GET.
	if _, path, found := strings.Cut(pattern, " "); found {
		return path
	}
	return pattern
}

// handleMetrics печатает экспозицию. Каталог читается на каждый опрос, а не
// кэшируется: одно чтение стоит десятки миллисекунд, а опрос идёт раз в
// несколько секунд — зато число всегда настоящее, а не то, каким было при
// старте.
func handleMetrics(m *metrics, q Querier, engine EngineInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Версия формата в Content-Type: без неё Prometheus разбирает ответ по
		// умолчанию, и смена формата однажды пройдёт молча.
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		fmt.Fprintf(w, "# HELP kbengine_build_info Version of the running binary; always 1.\n")
		fmt.Fprintf(w, "# TYPE kbengine_build_info gauge\n")
		fmt.Fprintf(w, "kbengine_build_info{version=%q,commit=%q} 1\n", engine.Version, engine.Commit)

		writeCatalogMetrics(w, q)
		m.write(w)
	}
}

// writeCatalogMetrics — размер каталога и число записей, которые не удалось
// прочитать. Второе важнее первого: движок поднимается с негодными записями и
// честно об этом говорит на /readyz, но там это видит только тот, кто смотрит
// прямо сейчас.
func writeCatalogMetrics(w io.Writer, q Querier) {
	st, err := q.Stats()
	if err != nil {
		// Отдельная метрика вместо молчания: пропущенный ряд на графике
		// выглядит как «сервис не отвечает», а это другой диагноз.
		fmt.Fprintf(w, "# HELP kbengine_catalog_readable Whether the catalog could be read at all.\n")
		fmt.Fprintf(w, "# TYPE kbengine_catalog_readable gauge\n")
		fmt.Fprintf(w, "kbengine_catalog_readable 0\n")
		return
	}
	fmt.Fprintf(w, "# HELP kbengine_catalog_readable Whether the catalog could be read at all.\n")
	fmt.Fprintf(w, "# TYPE kbengine_catalog_readable gauge\n")
	fmt.Fprintf(w, "kbengine_catalog_readable 1\n")

	fmt.Fprintf(w, "# HELP kbengine_catalog_entries Entries the catalog holds.\n")
	fmt.Fprintf(w, "# TYPE kbengine_catalog_entries gauge\n")
	fmt.Fprintf(w, "kbengine_catalog_entries %d\n", st.Total)

	fmt.Fprintf(w, "# HELP kbengine_catalog_unreadable_entries Entries present but rejected by validation.\n")
	fmt.Fprintf(w, "# TYPE kbengine_catalog_unreadable_entries gauge\n")
	fmt.Fprintf(w, "kbengine_catalog_unreadable_entries %d\n", len(st.Unreadable))
}

func (m *metrics) write(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Fprintf(w, "# HELP kbengine_http_requests_total Requests served, by route pattern and status.\n")
	fmt.Fprintf(w, "# TYPE kbengine_http_requests_total counter\n")
	// Порядок фиксирован: Prometheus его не требует, а человек, сравнивающий
	// два ответа глазами, требует.
	keys := slices.SortedFunc(maps.Keys(m.totals), func(a, b requestKey) int {
		return cmp.Or(cmp.Compare(a.route, b.route), cmp.Compare(a.code, b.code))
	})
	for _, k := range keys {
		fmt.Fprintf(w, "kbengine_http_requests_total{route=%q,code=%q} %d\n",
			k.route, strconv.Itoa(k.code), m.totals[k])
	}

	fmt.Fprintf(w, "# HELP kbengine_http_request_duration_seconds Time to serve a request.\n")
	fmt.Fprintf(w, "# TYPE kbengine_http_request_duration_seconds histogram\n")
	routes := slices.Sorted(maps.Keys(m.hist))
	for _, route := range routes {
		h := m.hist[route]
		for i, b := range bucketsSeconds {
			fmt.Fprintf(w, "kbengine_http_request_duration_seconds_bucket{route=%q,le=%q} %d\n",
				route, strconv.FormatFloat(b, 'g', -1, 64), h.counts[i])
		}
		// +Inf обязателен по формату: без него счётчик корзин не сходится с
		// _count, и запрос дольше пяти секунд просто исчезает из гистограммы.
		fmt.Fprintf(w, "kbengine_http_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", route, h.count)
		fmt.Fprintf(w, "kbengine_http_request_duration_seconds_sum{route=%q} %g\n", route, h.sum)
		fmt.Fprintf(w, "kbengine_http_request_duration_seconds_count{route=%q} %d\n", route, h.count)
	}
}
