package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/query"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

// Нагрузочный профиль: что происходит с ответами, когда запросов много СРАЗУ.
//
// Бенчмарки в проекте меряют одну операцию в одиночку — сколько стоит один
// разбор каталога, одно чтение книги. Это другой вопрос. Здесь спрашивается,
// во что превращается ответ, когда каталог читают одновременно двадцать раз:
// движок перечитывает файл на КАЖДЫЙ запрос (решение осознанное — правка
// каталога видна без перезапуска), и цена этого решения ни разу не измерялась.
//
// Инструмент — stdlib. Внешний генератор нагрузки (vegeta, k6, hey) дал бы
// красивее, но здесь нужен не отчёт, а ответ на один вопрос, и ради него
// заводить зависимость и второй язык в репозитории не стоит.
//
// ⚠️ В CI не ходит: по умолчанию тест пропускается. Он занимает секунды, а не
// миллисекунды, и его число зависит от машины — на общих раннерах это шум,
// который научатся игнорировать. Запуск: `KB_LOAD=1 go test ./internal/adapter/httpapi -run TestLoad -v`.
//
// ⚠️ Что этот стенд НЕ проверяет: сеть (клиент и сервер в одном процессе),
// параллельную ЗАПИСЬ, поведение за часы работы и память под нагрузкой.
// Он отвечает ровно на «как задержка чтения зависит от числа одновременных
// читателей».

const loadEnv = "KB_LOAD"

func skipUnlessLoad(t *testing.T) {
	t.Helper()
	if os.Getenv(loadEnv) == "" {
		t.Skipf("нагрузочный прогон пропущен: запустить с %s=1", loadEnv)
	}
}

// writeCatalog кладёт синтетический каталог заданного размера.
//
// Данные выдуманные намеренно: настоящий каталог владельца в репозиторий не
// попадает, а для вопроса «сколько стоит разбор N записей» содержание
// безразлично — важен объём.
func writeCatalog(t *testing.T, entries int) string {
	t.Helper()
	type entry struct {
		ID          int      `json:"id"`
		HabrID      int      `json:"habr_id"`
		Title       string   `json:"title"`
		URL         string   `json:"url"`
		Category    string   `json:"category"`
		Status      string   `json:"status"`
		Lifecycle   string   `json:"lifecycle"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Source      string   `json:"source"`
		Author      string   `json:"author"`
		DateAdded   string   `json:"date_added"`
	}
	doc := struct {
		Meta struct {
			Categories map[string]string `json:"categories"`
		} `json:"meta"`
		Entries []entry `json:"entries"`
	}{}
	doc.Meta.Categories = map[string]string{"testing": "Проверки: всё про тесты"}
	for i := range entries {
		doc.Entries = append(doc.Entries, entry{
			ID: i + 1, HabrID: 900000 + i,
			Title:       fmt.Sprintf("Запись номер %d про устройство проверок", i+1),
			URL:         fmt.Sprintf("https://example.invalid/articles/%d/", 900000+i),
			Category:    "testing",
			Status:      "keep",
			Lifecycle:   "active",
			Description: "Выдуманная запись нагрузочного стенда. Содержание безразлично, важен объём.",
			Tags:        []string{"тест", "нагрузка", "стенд"},
			Source:      "digest",
			Author:      "нагрузочный стенд",
			DateAdded:   "2026-08-23",
		})
	}
	path := filepath.Join(t.TempDir(), "catalog.json")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func loadServer(t *testing.T, entries int) *httptest.Server {
	t.Helper()
	loader := catalogjson.FileLoader{Path: writeCatalog(t, entries)}
	h := httpapi.NewServer(
		query.NewService(loader), audit.NewService(loader), analytics.NewService(loader),
		nil, nil, nil, httpapi.Documents{}, httpapi.EngineInfo{Version: "load"}, nil,
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type profile struct {
	requests int
	// Транспортная ошибка и ответ не-200 — РАЗНЫЕ вещи, и путать их нельзя.
	// Первая на локальном стенде означает исчерпание сокетов у клиента при
	// десятках тысяч запросов в секунду, то есть свойство машины; вторая —
	// что сервер под нагрузкой начал отвечать неправильно. Считать их вместе
	// значит регулярно обвинять движок в том, чего он не делал, а детектор,
	// который ругается на правду, приучаются игнорировать.
	netErrors          int
	badStatus          int
	p50, p95, p99, max time.Duration
	rps                float64
}

// measure держит `workers` клиентов, каждый из которых шлёт запросы подряд,
// и возвращает распределение задержек.
//
// Считается ВСЁ время ответа, включая чтение тела: сервер, отдающий заголовки
// быстро и тело медленно, снаружи медленный, и мерить до первого байта значило
// бы измерять не то, что чувствует человек.
func measure(t *testing.T, url string, workers int, dur time.Duration) profile {
	t.Helper()
	var (
		mu     sync.Mutex
		lat    []time.Duration
		netErr atomic.Int64
		bad    atomic.Int64
		wg     sync.WaitGroup
	)
	client := &http.Client{Timeout: 30 * time.Second}
	deadline := time.Now().Add(dur)
	start := time.Now()
	for range workers {
		wg.Go(func() {
			var local []time.Duration
			for time.Now().Before(deadline) {
				t0 := time.Now()
				resp, err := client.Get(url)
				if err != nil {
					netErr.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					bad.Add(1)
					continue
				}
				local = append(local, time.Since(t0))
			}
			mu.Lock()
			lat = append(lat, local...)
			mu.Unlock()
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	slices.Sort(lat)
	at := func(q float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		i := int(float64(len(lat)-1) * q)
		return lat[i]
	}
	p := profile{requests: len(lat), netErrors: int(netErr.Load()), badStatus: int(bad.Load()),
		p50: at(0.50), p95: at(0.95), p99: at(0.99)}
	if len(lat) > 0 {
		p.max = lat[len(lat)-1]
		p.rps = float64(len(lat)) / elapsed.Seconds()
	}
	return p
}

// TestLoad_catalogUnderConcurrency — как растёт задержка чтения каталога с
// числом одновременных читателей.
//
// Ответ читается по ФОРМЕ кривой, а не по абсолютным числам: если задержка
// растёт линейно с числом клиентов при неизменном RPS — работа сериализована,
// и один медленный запрос держит остальные. Если RPS растёт, а задержка стоит —
// параллелится честно.
func TestLoad_catalogUnderConcurrency(t *testing.T) {
	skipUnlessLoad(t)
	srv := loadServer(t, 1500)

	t.Logf("%-10s %8s %8s %8s %8s %9s %7s", "клиентов", "запросов", "p50", "p95", "p99", "RPS", "не-200")
	for _, workers := range []int{1, 4, 16, 64} {
		p := measure(t, srv.URL+"/api/entries", workers, 2*time.Second)
		t.Logf("%-10d %8d %8s %8s %8s %9.0f %7d",
			workers, p.requests, p.p50.Round(time.Microsecond), p.p95.Round(time.Microsecond),
			p.p99.Round(time.Microsecond), p.rps, p.badStatus)
		if p.badStatus > 0 {
			t.Errorf("при %d клиентах %d ответов не 200 — под нагрузкой отдача ломается", workers, p.badStatus)
		}
		if p.netErrors > 0 {
			t.Logf("    (%d транспортных ошибок — клиент и сервер в одном процессе, при таком темпе это сокеты машины, не движок)", p.netErrors)
		}
	}
}

// TestLoad_catalogSizeCost — как задержка зависит от РАЗМЕРА каталога при
// одном и том же числе клиентов.
//
// Вопрос не праздный: каталог перечитывается на каждый запрос, поэтому его рост
// оплачивается каждым посетителем, а не один раз при старте.
func TestLoad_catalogSizeCost(t *testing.T) {
	skipUnlessLoad(t)

	t.Logf("%-10s %8s %8s %8s %9s", "записей", "запросов", "p50", "p95", "RPS")
	for _, size := range []int{100, 500, 1500, 5000} {
		srv := loadServer(t, size)
		p := measure(t, srv.URL+"/api/entries", 8, 2*time.Second)
		t.Logf("%-10d %8d %8s %8s %9.0f",
			size, p.requests, p.p50.Round(time.Microsecond), p.p95.Round(time.Microsecond), p.rps)
	}
}

// TestLoad_routeMix — не все маршруты стоят одинаково, и дорогой сосед виден
// только когда его спрашивают вместе с остальными.
func TestLoad_routeMix(t *testing.T) {
	skipUnlessLoad(t)
	srv := loadServer(t, 1500)

	t.Logf("%-18s %8s %8s %8s %9s", "маршрут", "запросов", "p50", "p95", "RPS")
	for _, route := range []string{"/api/entries", "/api/stats", "/api/audits", "/api/search?q=проверок", "/api/engine"} {
		p := measure(t, srv.URL+route, 8, 2*time.Second)
		t.Logf("%-18s %8d %8s %8s %9.0f",
			route, p.requests, p.p50.Round(time.Microsecond), p.p95.Round(time.Microsecond), p.rps)
		if p.badStatus > 0 {
			t.Errorf("%s: %d ответов не 200", route, p.badStatus)
		}
		if p.netErrors > 0 {
			t.Logf("    (%s: %d транспортных ошибок — сокеты машины, не движок)", route, p.netErrors)
		}
	}
}
