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

// Purpose names why a call was made, by its reply.
type Purpose func(chat.ModelReply) domain.CallPurpose

// AnswerOrSearch is the purpose of a chat call: модель вызвала инструмент —
// это шаг поиска, иначе ответ.
func AnswerOrSearch(r chat.ModelReply) domain.CallPurpose {
	if len(r.ToolCalls) > 0 {
		return domain.CallSearch
	}
	return domain.CallAnswer
}

// Fixed is a purpose known before the call (разбор, сжатие портрета).
func Fixed(p domain.CallPurpose) Purpose {
	return func(chat.ModelReply) domain.CallPurpose { return p }
}

// Meter is a chat.Model and chat.Streamer that records every call it passes on.
type Meter struct {
	next    chat.Model
	model   string
	purpose Purpose
	rec     Recorder
	now     func() time.Time
	report  func(error) // сбой записи: ответ человеку важнее счета
}

// NewMeter wraps next; report gets record failures (nil — молча).
func NewMeter(next chat.Model, model string, purpose Purpose, rec Recorder, now func() time.Time, report func(error)) *Meter {
	return &Meter{next: next, model: model, purpose: purpose, rec: rec, now: now, report: report}
}

// Complete implements chat.Model.
func (m *Meter) Complete(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool) (chat.ModelReply, error) {
	start := m.now()
	r, err := m.next.Complete(ctx, msgs, tools)
	took := m.now().Sub(start)
	m.record(start, r, took, took, err)
	return r, err
}

// Stream implements chat.Streamer; модель без потока отвечает целиком, и
// ответ показывается одним куском.
func (m *Meter) Stream(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool, show func(string)) (chat.ModelReply, error) {
	s, ok := m.next.(chat.Streamer)
	if !ok {
		r, err := m.Complete(ctx, msgs, tools)
		if err == nil && r.Content != "" {
			show(r.Content)
		}
		return r, err
	}
	start := m.now()
	var first time.Duration = -1
	r, err := s.Stream(ctx, msgs, tools, func(text string) {
		if first < 0 && text != "" {
			first = m.now().Sub(start)
		}
		show(text)
	})
	took := m.now().Sub(start)
	if first < 0 || first > took {
		first = took // ни слова текста — только вызовы инструментов
	}
	m.record(start, r, first, took, err)
	return r, err
}

func (m *Meter) record(at time.Time, r chat.ModelReply, first, took time.Duration, err error) {
	c, cerr := domain.NewModelCall(at, m.purpose(r), m.model, r.Usage.In, r.Usage.Out, first, took, err != nil)
	if cerr == nil {
		cerr = m.rec.Record(c)
	}
	if cerr != nil && m.report != nil {
		m.report(cerr)
	}
}
