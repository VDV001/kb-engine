// Package usage counts model calls for the window statistics (решение
// владельца 07.11): каждый вызов модели — назначение, токены, время.
package usage

import (
	"context"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// Recorder keeps model calls.
type Recorder interface {
	Record(c domain.ModelCall) error
}

// Purpose names why a call was made: by the tools offered and the reply
// (failed — ответа нет).
type Purpose func(offered []chat.Tool, r chat.ModelReply, failed bool) domain.CallPurpose

// AnswerOrSearch is the purpose of a chat call: модель вызвала инструмент —
// это шаг поиска, иначе ответ.
// ponytail: неудачный шаг, где модели давали инструменты, считается поиском —
// по пустому ответу назначение не узнать. Потолок: упавший последний ответ в
// режиме «модель ищет» попадет в поиск. Путь: чат передает номер шага.
func AnswerOrSearch(offered []chat.Tool, r chat.ModelReply, failed bool) domain.CallPurpose {
	if len(r.ToolCalls) > 0 || (failed && len(offered) > 0) {
		return domain.CallSearch
	}
	return domain.CallAnswer
}

// Fixed is a purpose known before the call (разбор, сжатие портрета).
func Fixed(p domain.CallPurpose) Purpose {
	return func([]chat.Tool, chat.ModelReply, bool) domain.CallPurpose { return p }
}

// Meter is a chat.Model that records every call it passes on.
type Meter struct {
	next    chat.Model
	model   string
	purpose Purpose
	rec     Recorder
	now     func() time.Time
	report  func(error) // сбой записи: ответ человеку важнее счета
}

// NewMeter wraps next; report gets record failures (nil — молча). Обертка
// умеет поток (chat.Streamer), только если умеет next: чат по этому признаку
// решает, показывать ли ответ кусками.
func NewMeter(next chat.Model, model string, purpose Purpose, rec Recorder, now func() time.Time, report func(error)) chat.Model {
	m := &Meter{next: next, model: model, purpose: purpose, rec: rec, now: now, report: report}
	if s, ok := next.(chat.Streamer); ok {
		return &streamMeter{Meter: m, next: s}
	}
	return m
}

// streamMeter is a Meter over a model that streams.
type streamMeter struct {
	*Meter
	next chat.Streamer
}

// Complete implements chat.Model.
func (m *Meter) Complete(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool) (chat.ModelReply, error) {
	start := m.now()
	r, err := m.next.Complete(ctx, msgs, tools)
	took := m.now().Sub(start)
	m.record(start, tools, r, took, took, err)
	return r, err
}

// Stream implements chat.Streamer.
func (m *streamMeter) Stream(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool, show func(string)) (chat.ModelReply, error) {
	start := m.now()
	var first time.Duration = -1
	r, err := m.next.Stream(ctx, msgs, tools, func(text string) {
		if first < 0 && text != "" {
			first = m.now().Sub(start)
		}
		show(text)
	})
	took := m.now().Sub(start)
	if first < 0 || first > took {
		first = took // ни слова текста — только вызовы инструментов
	}
	m.record(start, tools, r, first, took, err)
	return r, err
}

func (m *Meter) record(at time.Time, tools []chat.Tool, r chat.ModelReply, first, took time.Duration, err error) {
	c, cerr := domain.NewModelCall(at, m.purpose(tools, r, err != nil), m.model, r.Usage.In, r.Usage.Out, r.Usage.Counted, first, took, err != nil)
	if cerr == nil {
		cerr = m.rec.Record(c)
	}
	if cerr != nil && m.report != nil {
		m.report(cerr)
	}
}
