package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server-Timing — разбивка ОДНОГО ответа по шагам.
//
// Метрика отвечает «медленно ли», но не «где»: чтение каталога, сводка
// финансов или кодирование ответа. Заголовок закрывает этот разрыв и стоит
// ноль зависимостей: формат стандартный, браузер показывает разбивку прямо во
// вкладке Network, а curl видит обычную строку.
//
// ponytail: это замер шагов ВНУТРИ одного процесса, а не распределённая
// трассировка. Потолок — нет идентификатора трассы, нет связи между
// процессами, нет хранения истории. Когда у движка появится второй процесс или
// внешний вызов, брать OpenTelemetry (замерено 22.08: +3 прямые зависимости,
// ~26 модулей, +16,6 МБ к бинарю), а не дописывать это.

type timingKey struct{}

// timeline собирает длительности шагов одного запроса.
//
// Мьютекс не украшение: обработчик вправе считать части ответа параллельно, и
// тогда шаги пишутся из разных горутин.
type timeline struct {
	mu    sync.Mutex
	steps []step
}

type step struct {
	name string
	dur  time.Duration
}

// track отмечает шаг под именем name; возвращённую функцию вызывают, когда шаг
// закончился. Без включённого заголовка возвращается пустышка — вызывающему не
// нужно знать, включён ли замер.
func track(ctx context.Context, name string) func() {
	tl, ok := ctx.Value(timingKey{}).(*timeline)
	if !ok {
		return func() {}
	}
	start := time.Now()
	return func() {
		d := time.Since(start)
		tl.mu.Lock()
		defer tl.mu.Unlock()
		tl.steps = append(tl.steps, step{name: name, dur: d})
	}
}

// header собирает значение заголовка. total идёт первым: без него шаги не с чем
// сравнивать, и «где медленно» снова остаётся без ответа.
func (t *timeline) header(total time.Duration) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := make([]string, 0, len(t.steps)+1)
	parts = append(parts, fmt.Sprintf("total;dur=%.1f", ms(total)))
	for _, s := range t.steps {
		parts = append(parts, fmt.Sprintf("%s;dur=%.1f", s.name, ms(s.dur)))
	}
	return strings.Join(parts, ", ")
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// withTimeline вешает сборщик шагов на запрос.
func withTimeline(r *http.Request) (*http.Request, *timeline) {
	tl := &timeline{}
	return r.WithContext(context.WithValue(r.Context(), timingKey{}, tl)), tl
}

// timingWriter ставит заголовок в последний момент, когда его ещё можно
// поставить: заголовки уходят с первым Write, и дописать что-либо после
// обработчика уже нельзя.
//
// ⚠️ Отсюда честная граница: total здесь — время ДО начала записи тела.
// Кодирование ответа в него не входит, потому что в момент, когда его можно
// было бы измерить, заголовок уже отправлен. Шаги, которые обработчик успел
// отметить до первой записи (чтение каталога, сводка, фильтрация), входят
// полностью — а это ровно те, ради которых заголовок и заводился.
type timingWriter struct {
	http.ResponseWriter
	tl    *timeline
	start time.Time
	sent  bool
}

func (w *timingWriter) send() {
	if w.sent {
		return
	}
	w.sent = true
	w.Header().Set("Server-Timing", w.tl.header(time.Since(w.start)))
}

func (w *timingWriter) WriteHeader(code int) {
	w.send()
	w.ResponseWriter.WriteHeader(code)
}

func (w *timingWriter) Write(b []byte) (int, error) {
	w.send()
	return w.ResponseWriter.Write(b)
}
