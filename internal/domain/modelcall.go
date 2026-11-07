package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Вызов модели для статистики окна DVK (решение владельца 07.11): зачем
// звали, какая модель, сколько токенов ушло и пришло, как быстро. Текста
// вопроса и ответа здесь нет по устройству.

// ErrInvalidModelCall — вызов нарушает инвариант (нет модели, токены < 0…).
var ErrInvalidModelCall = errors.New("invalid model call")

// CallPurpose — зачем окно позвало модель.
type CallPurpose string

// Назначения — только те, что окно уже зовет.
const (
	CallAnswer   CallPurpose = "answer"   // ответ в чате
	CallSearch   CallPurpose = "search"   // шаг, где модель ищет и читает документы
	CallExtract  CallPurpose = "extract"  // разбор ответа для портрета
	CallCompress CallPurpose = "compress" // сжатие портрета
)

var callPurposes = []CallPurpose{CallAnswer, CallSearch, CallExtract, CallCompress}

// ModelCall is one request to a model.
type ModelCall struct {
	at         time.Time
	purpose    CallPurpose
	model      string
	in, out    int
	counted    bool // провайдер прислал счет; нет — in/out неизвестны, а не нули
	firstToken time.Duration
	took       time.Duration
	failed     bool
}

// NewModelCall validates a call: counted — провайдер прислал счет токенов;
// firstToken — до первого слова ответа (без потока — весь ответ), took — до
// конца; первое слово не позже конца.
func NewModelCall(at time.Time, purpose CallPurpose, model string, in, out int, counted bool, firstToken, took time.Duration, failed bool) (ModelCall, error) {
	model = strings.TrimSpace(model)
	switch {
	case at.IsZero():
		return ModelCall{}, fmt.Errorf("%w: time is required", ErrInvalidModelCall)
	case !slices.Contains(callPurposes, purpose):
		return ModelCall{}, fmt.Errorf("%w: purpose %q", ErrInvalidModelCall, purpose)
	case model == "":
		return ModelCall{}, fmt.Errorf("%w: model is required", ErrInvalidModelCall)
	case in < 0 || out < 0:
		return ModelCall{}, fmt.Errorf("%w: tokens %d/%d", ErrInvalidModelCall, in, out)
	case took < 0 || firstToken < 0 || firstToken > took:
		return ModelCall{}, fmt.Errorf("%w: durations %v/%v", ErrInvalidModelCall, firstToken, took)
	}
	return ModelCall{at: at, purpose: purpose, model: model, in: in, out: out, counted: counted, firstToken: firstToken, took: took, failed: failed}, nil
}

// At returns when the call started.
func (c ModelCall) At() time.Time { return c.at }

// Purpose returns why the window called the model.
func (c ModelCall) Purpose() CallPurpose { return c.purpose }

// Model returns the model name from the settings.
func (c ModelCall) Model() string { return c.model }

// In returns the input tokens the provider counted.
func (c ModelCall) In() int { return c.in }

// Out returns the output tokens the provider counted.
func (c ModelCall) Out() int { return c.out }

// Counted reports whether the provider sent its token count.
func (c ModelCall) Counted() bool { return c.counted }

// FirstToken returns the time to the first word of the answer.
func (c ModelCall) FirstToken() time.Duration { return c.firstToken }

// Took returns the time of the whole call.
func (c ModelCall) Took() time.Duration { return c.took }

// Failed reports whether the call ended with an error.
func (c ModelCall) Failed() bool { return c.failed }
