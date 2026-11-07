package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

var callAt = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// Вызов модели — для статистики окна: зачем звали, какая модель, токены на
// вход и на выход, время до первого слова и всего, неудача. Текста нет.
func TestNewModelCall(t *testing.T) {
	ok, err := domain.NewModelCall(callAt, domain.CallAnswer, " anthropic/claude-sonnet-5.5 ", 5200, 640, 800*time.Millisecond, 3*time.Second, false)
	if err != nil || ok.Model() != "anthropic/claude-sonnet-5.5" || ok.In() != 5200 || ok.Out() != 640 || ok.Purpose() != domain.CallAnswer ||
		ok.FirstToken() != 800*time.Millisecond || ok.Took() != 3*time.Second || ok.Failed() || !ok.At().Equal(callAt) {
		t.Fatalf("вызов = %+v, %v", ok, err)
	}
	for _, c := range []struct {
		name        string
		at          time.Time
		purpose     domain.CallPurpose
		model       string
		in, out     int
		first, took time.Duration
	}{
		{"без времени", time.Time{}, domain.CallAnswer, "m", 1, 1, 0, time.Second},
		{"чужое назначение", callAt, domain.CallPurpose("chat"), "m", 1, 1, 0, time.Second},
		{"без модели", callAt, domain.CallAnswer, "  ", 1, 1, 0, time.Second},
		{"токены меньше нуля", callAt, domain.CallAnswer, "m", -1, 1, 0, time.Second},
		{"выход меньше нуля", callAt, domain.CallAnswer, "m", 1, -1, 0, time.Second},
		{"время меньше нуля", callAt, domain.CallAnswer, "m", 1, 1, 0, -time.Second},
		{"первое слово позже конца", callAt, domain.CallAnswer, "m", 1, 1, 2 * time.Second, time.Second},
	} {
		if _, err := domain.NewModelCall(c.at, c.purpose, c.model, c.in, c.out, c.first, c.took, false); !errors.Is(err, domain.ErrInvalidModelCall) {
			t.Errorf("%s: принят (%v)", c.name, err)
		}
	}
}

// Назначения — только те, что окно уже зовет: ответ, поиск моделью, разбор
// и сжатие портрета.
func TestNewCallPurpose(t *testing.T) {
	for _, raw := range []string{"answer", "search", "extract", "compress"} {
		if p, err := domain.NewCallPurpose(raw); err != nil || string(p) != raw {
			t.Errorf("%q: %q %v", raw, p, err)
		}
	}
	if _, err := domain.NewCallPurpose("tts"); !errors.Is(err, domain.ErrInvalidModelCall) {
		t.Fatalf("неизвестное назначение принято: %v", err)
	}
}
