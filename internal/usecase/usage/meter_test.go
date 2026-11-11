package usage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

type memRec struct{ calls []domain.ModelCall }

func (m *memRec) Record(c domain.ModelCall) error { m.calls = append(m.calls, c); return nil }

// clock отдает моменты по очереди: начало, первое слово, конец.
type clock struct{ t []time.Time }

func (c *clock) now() time.Time {
	t := c.t[0]
	if len(c.t) > 1 {
		c.t = c.t[1:]
	}
	return t
}

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

type fakeModel struct {
	reply chat.ModelReply
	err   error
}

func (f fakeModel) Complete(context.Context, []chat.ModelMessage, []chat.Tool) (chat.ModelReply, error) {
	return f.reply, f.err
}

type fakeStreamer struct{ fakeModel }

func (f fakeStreamer) Stream(_ context.Context, _ []chat.ModelMessage, _ []chat.Tool, show func(string)) (chat.ModelReply, error) {
	show("")       // пустой кусок — еще не слово
	show("Сейчас") // первое слово
	return f.reply, f.err
}

// Счетчик пишет каждый вызов модели: назначение по ответу (вызвал инструмент —
// «поиск моделью», иначе «ответ»), токены из ответа, время до первого слова
// и всего; ответ отдает как есть.
func TestMeter(t *testing.T) {
	q := []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}
	for _, tc := range []struct {
		name    string
		model   chat.Model
		stream  bool
		purpose domain.CallPurpose
		first   time.Duration
		took    time.Duration
	}{
		{"ответ целиком", fakeModel{reply: chat.ModelReply{Content: "ок", Usage: chat.Usage{In: 5200, Out: 640, Counted: true}}}, false, domain.CallAnswer, 3 * time.Second, 3 * time.Second},
		{"шаг поиска", fakeModel{reply: chat.ModelReply{ToolCalls: []chat.ToolCall{{ID: "1", Name: "search"}}, Usage: chat.Usage{In: 5200, Out: 640, Counted: true}}}, false, domain.CallSearch, 3 * time.Second, 3 * time.Second},
		{"поток", fakeStreamer{fakeModel{reply: chat.ModelReply{Content: "Сейчас", Usage: chat.Usage{In: 5200, Out: 640, Counted: true}}}}, true, domain.CallAnswer, time.Second, 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &memRec{}
			times := []time.Time{t0, t0.Add(3 * time.Second)}
			if tc.first != tc.took {
				times = []time.Time{t0, t0.Add(tc.first), t0.Add(tc.took)}
			}
			c := &clock{t: times}
			m := usage.NewMeter(tc.model, "anthropic/claude-sonnet-5.5", usage.AnswerOrSearch, rec, c.now, nil)
			var got chat.ModelReply
			var err error
			var shown []string
			if tc.stream {
				st, ok := m.(chat.Streamer)
				if !ok {
					t.Fatal("счетчик потоковой модели — не поток")
				}
				got, err = st.Stream(t.Context(), q, nil, func(s string) { shown = append(shown, s) })
			} else {
				got, err = m.Complete(t.Context(), q, nil)
			}
			if err != nil || got.Usage.In != 5200 {
				t.Fatalf("ответ = %+v, %v", got, err)
			}
			if tc.stream && (len(shown) == 0 || shown[len(shown)-1] == "") {
				t.Fatalf("показ не дошел: %q", shown)
			}
			if len(rec.calls) != 1 {
				t.Fatalf("записей %d", len(rec.calls))
			}
			r := rec.calls[0]
			if r.Purpose() != tc.purpose || r.In() != 5200 || r.Out() != 640 || r.Model() != "anthropic/claude-sonnet-5.5" ||
				!r.Counted() || r.FirstToken() != tc.first || r.Took() != tc.took || r.Failed() || !r.At().Equal(t0) {
				t.Fatalf("запись = %+v", r)
			}
		})
	}
}

// Неудачный вызов тоже пишется (без токенов), ошибка уходит дальше как есть;
// назначение задано — разбор портрета.
func TestMeter_FailedAndFixedPurpose(t *testing.T) {
	boom := errors.New("429")
	rec := &memRec{}
	c := &clock{t: []time.Time{t0, t0.Add(2 * time.Second)}}
	m := usage.NewMeter(fakeModel{err: boom}, "m", usage.Fixed(domain.CallExtract), rec, c.now, nil)
	if _, err := m.Complete(t.Context(), nil, nil); !errors.Is(err, boom) {
		t.Fatalf("ошибка = %v", err)
	}
	if len(rec.calls) != 1 || !rec.calls[0].Failed() || rec.calls[0].Purpose() != domain.CallExtract || rec.calls[0].In() != 0 {
		t.Fatalf("записи = %+v", rec.calls)
	}
}

type brokenRec struct{}

func (brokenRec) Record(domain.ModelCall) error { return errors.New("disk full") }

// Журнал не записался — ответ человеку все равно идет, а сбой уходит в report.
func TestMeter_RecordFailureDoesNotBreakAnswer(t *testing.T) {
	var reported error
	c := &clock{t: []time.Time{t0, t0.Add(time.Second)}}
	m := usage.NewMeter(fakeModel{reply: chat.ModelReply{Content: "ок"}}, "m", usage.AnswerOrSearch, brokenRec{}, c.now, func(err error) { reported = err })
	got, err := m.Complete(t.Context(), nil, nil)
	if err != nil || got.Content != "ок" || reported == nil {
		t.Fatalf("ответ %+v, ошибка %v, сообщено %v", got, err, reported)
	}
}

// Модель без потока остается без потока и за счетчиком: чат по этому
// признаку не показывает ответ кусками (ревью #491).
func TestMeter_StreamerOnlyIfInnerStreams(t *testing.T) {
	rec := &memRec{}
	if _, ok := usage.NewMeter(fakeModel{}, "m", usage.AnswerOrSearch, rec, time.Now, nil).(chat.Streamer); ok {
		t.Fatal("счетчик модели без потока притворяется потоком")
	}
	if _, ok := usage.NewMeter(fakeStreamer{}, "m", usage.AnswerOrSearch, rec, time.Now, nil).(chat.Streamer); !ok {
		t.Fatal("счетчик потоковой модели — не поток")
	}
}

// Неудачный шаг, где модели давали инструменты, — шаг поиска: ответа нет, и
// по нему назначение не узнать (ревью #491).
func TestMeter_FailedStepWithTools(t *testing.T) {
	rec := &memRec{}
	c := &clock{t: []time.Time{t0, t0.Add(time.Second)}}
	m := usage.NewMeter(fakeModel{err: errors.New("timeout")}, "m", usage.AnswerOrSearch, rec, c.now, nil)
	_, _ = m.Complete(t.Context(), nil, []chat.Tool{{Name: "search"}})
	if len(rec.calls) != 1 || rec.calls[0].Purpose() != domain.CallSearch || rec.calls[0].Counted() {
		t.Fatalf("записи = %+v", rec.calls)
	}
}

// Счетчик с чатом пишет его в каждый вызов; без чата — вызов вне чата.
func TestMeter_Chat(t *testing.T) {
	q := []chat.ModelMessage{{Role: chat.RoleUser, Content: "q"}}
	for _, tc := range []struct {
		name string
		opts []usage.MeterOption
		want string
	}{{"с чатом", []usage.MeterOption{usage.WithChat("0123456789abcdef")}, "0123456789abcdef"}, {"без чата", nil, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &memRec{}
			m := usage.NewMeter(fakeModel{reply: chat.ModelReply{Content: "ok", Usage: chat.Usage{In: 10, Out: 5, Counted: true}}}, "m", usage.AnswerOrSearch, rec, (&clock{t: []time.Time{t0}}).now, nil, tc.opts...)
			if _, err := m.Complete(t.Context(), q, nil); err != nil {
				t.Fatal(err)
			}
			if len(rec.calls) != 1 || rec.calls[0].Chat() != tc.want {
				t.Fatalf("записано %+v, чат ждали %q", rec.calls, tc.want)
			}
		})
	}
}
