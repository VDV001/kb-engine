// Package monium ships the server's error lines to Monium over OTLP JSON (#440).
// Cloud Logging is closed to new clients (22.10.2026) and the container's stdout
// never reaches Monium on its own, so the server sends its errors itself.
package monium

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ErrConfig — не хватает адреса, ключа или проекта: отправка выключена.
var ErrConfig = errors.New("monium: endpoint, api key and project are required")

const (
	// DefaultQueue is how many lines wait for sending before new ones are dropped.
	DefaultQueue = 256
	// maxBatch bounds the records of one request.
	maxBatch = 100
	// sendTimeout bounds one request, и остановку: перед выходом Run досылает остаток.
	sendTimeout = 5 * time.Second

	severityError = 17 // OTLP SeverityNumber ERROR
)

// Config of the sender. Cluster and Service empty — Monium takes "default".
type Config struct {
	Endpoint string
	APIKey   string
	Project  string
	Cluster  string
	Service  string
	Client   *http.Client // nil — клиент с таймаутом sendTimeout
	Now      func() time.Time
	Queue    int // 0 — DefaultQueue
	// FlushTimeout bounds the whole send of what is left when Run stops; 0 — 8 s.
	FlushTimeout time.Duration
}

type record struct {
	at   time.Time
	line string
}

// Writer is an io.Writer for log.SetOutput: every line goes to Monium as ERROR.
//
// ponytail: доставка без повторов — недоступный Monium или ответ не 2xx теряют
// пачку молча (писать об этом в log нельзя: он и есть этот Writer). Потолок —
// сбой дольше секунд; путь вверх — ограниченные повторы и счетчик потерь в /health.
type Writer struct {
	cfg     Config
	ch      chan record
	dropped atomic.Uint64
}

// New checks the config and builds the writer; без Run ничего не отправляется.
func New(cfg Config) (*Writer, error) {
	if cfg.Endpoint == "" || cfg.APIKey == "" || cfg.Project == "" {
		return nil, ErrConfig
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: sendTimeout}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Queue <= 0 {
		cfg.Queue = DefaultQueue
	}
	return &Writer{cfg: cfg, ch: make(chan record, cfg.Queue)}, nil
}

// Вычистка секретов (ревью #696). Строка ошибки Go печатает URL, заголовки и JSON как
// есть, поэтому правил несколько, и все они режут значение, а не имя. Лучше лишнее
// «[redacted]», чем ключ в журнале.
var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	// Authorization: <схема> <значение>, схема любая (Bearer, Basic, Token).
	{regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*)(?:[a-z]+\s+)?[^\s,;"']+`), "${1}[redacted]"},
	// Схема и значение без заголовка: «Bearer abc…», «Api-Key AQV…». Значение от 8 знаков,
	// чтобы «the bearer of bad news» осталось текстом.
	{regexp.MustCompile(`(?i)\b(bearer|basic|api-key|application-key)\s+[^\s,;"']{8,}`), "${1} [redacted]"},
	// Пароль в адресе: scheme://user:пароль@host.
	{regexp.MustCompile(`(://[^/\s:@]+:)[^@\s/]+@`), "${1}[redacted]@"},
	// имя = значение и "имя": "значение", где в имени слово из списка (x-api-key, access_key,
	// secret_key, password, session, cookie, token). Значение в кавычках режется целиком.
	{regexp.MustCompile(`(?i)(["']?[\w.-]*(?:token|key|secret|passw(?:or)?d|pwd|credential|cookie|session)[\w.-]*["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s&,;}"']+)`), "${1}[redacted]"},
}

func redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}

// Write queues the line and never blocks or fails: the server must not wait for Monium.
func (w *Writer) Write(p []byte) (int, error) {
	line := redact(strings.TrimRight(string(p), "\r\n"))
	if line == "" {
		return len(p), nil
	}
	select {
	case w.ch <- record{at: w.cfg.Now(), line: line}:
	default:
		w.dropped.Add(1)
	}
	return len(p), nil
}

// Dropped is how many lines were thrown away because the queue was full.
func (w *Writer) Dropped() uint64 { return w.dropped.Load() }

// Run sends the queue until ctx ends, then sends what is left.
func (w *Writer) Run(ctx context.Context) {
	for {
		select {
		case r := <-w.ch:
			w.send(ctx, w.batch(r))
		case <-ctx.Done():
			for {
				select {
				case r := <-w.ch:
					w.send(ctx, w.batch(r))
				default:
					return
				}
			}
		}
	}
}

// batch takes what is already queued behind first, up to maxBatch.
func (w *Writer) batch(first record) []record {
	b := []record{first}
	for len(b) < maxBatch {
		select {
		case r := <-w.ch:
			b = append(b, r)
		default:
			return b
		}
	}
	return b
}

type (
	otlp struct {
		ResourceLogs []resourceLogs `json:"resourceLogs"`
	}
	resourceLogs struct {
		ScopeLogs []scopeLogs `json:"scopeLogs"`
	}
	scopeLogs struct {
		LogRecords []logRecord `json:"logRecords"`
	}
	logRecord struct {
		TimeUnixNano   string   `json:"timeUnixNano"`
		SeverityNumber int      `json:"severityNumber"`
		SeverityText   string   `json:"severityText"`
		Body           anyValue `json:"body"`
	}
	anyValue struct {
		StringValue string `json:"stringValue"`
	}
)

// send posts one batch; сбой теряет пачку (см. ponytail у Writer). Отмена Run
// запрос не обрывает: остановка досылает хвост.
func (w *Writer) send(ctx context.Context, b []record) {
	recs := make([]logRecord, len(b))
	for i, r := range b {
		recs[i] = logRecord{TimeUnixNano: strconv.FormatInt(r.at.UnixNano(), 10), SeverityNumber: severityError, SeverityText: "ERROR", Body: anyValue{r.line}}
	}
	body, err := json.Marshal(otlp{[]resourceLogs{{[]scopeLogs{{recs}}}}})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+w.cfg.APIKey)
	req.Header.Set("x-monium-project", w.cfg.Project)
	if w.cfg.Cluster != "" {
		req.Header.Set("x-monium-cluster", w.cfg.Cluster)
	}
	if w.cfg.Service != "" {
		req.Header.Set("x-monium-service", w.cfg.Service)
	}
	resp, err := w.cfg.Client.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
