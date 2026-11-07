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
	ok, err := domain.NewModelCall(callAt, domain.CallAnswer, " anthropic/claude-sonnet-5.5 ", 5200, 640, true, 800*time.Millisecond, 3*time.Second, false)
	if err != nil || ok.Model() != "anthropic/claude-sonnet-5.5" || ok.In() != 5200 || ok.Out() != 640 || ok.Purpose() != domain.CallAnswer ||
		!ok.Counted() || ok.FirstToken() != 800*time.Millisecond || ok.Took() != 3*time.Second || ok.Failed() || !ok.At().Equal(callAt) {
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
		if _, err := domain.NewModelCall(c.at, c.purpose, c.model, c.in, c.out, true, c.first, c.took, false); !errors.Is(err, domain.ErrInvalidModelCall) {
			t.Errorf("%s: принят (%v)", c.name, err)
		}
	}
}

// Без счета токены неизвестны, а не числа: вызов без счета их не отдает, даже
// если пришли (правка журнала руками, сервер прислал мусор). Правило в домене,
// чтобы каждый потребитель — сводка, окно, журнал — не обнулял их сам.
func TestNewModelCall_uncountedHasNoTokens(t *testing.T) {
	c, err := domain.NewModelCall(callAt, domain.CallAnswer, "qwen3", 7000, 700, false, 0, time.Second, false)
	if err != nil || c.Counted() || c.In() != 0 || c.Out() != 0 {
		t.Fatalf("вызов без счета = in %d out %d counted %v, %v", c.In(), c.Out(), c.Counted(), err)
	}
}

// Облачная речь (#493) — свои назначения: распознавание диктовки и облачный
// голос идут через тот же шлюз и тратят те же деньги.
func TestNewModelCall_speechPurposes(t *testing.T) {
	for _, p := range []domain.CallPurpose{domain.CallListen, domain.CallSpeak} {
		if _, err := domain.NewModelCall(callAt, p, "openai/gpt-4o-mini-transcribe", 0, 0, false, time.Second, time.Second, false); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
}

// Шлюз за облачную речь присылает не токены, а цену вызова в долларах (замер
// 07.11: /transcription-model и /speech-model, providerMetadata.gateway.cost).
// Такой вызов — со счетом в деньгах: цена известна, токены нет.
func TestNewBilledModelCall(t *testing.T) {
	c, err := domain.NewBilledModelCall(callAt, domain.CallListen, " openai/gpt-4o-mini-transcribe ", 96_250_000, 2*time.Second, false)
	if err != nil || !c.Billed() || c.Cost() != 96_250_000 || c.Counted() || c.In() != 0 || c.Out() != 0 ||
		c.Model() != "openai/gpt-4o-mini-transcribe" || c.Purpose() != domain.CallListen || c.Took() != 2*time.Second || c.FirstToken() != 2*time.Second {
		t.Fatalf("вызов = %+v, %v", c, err)
	}
	if free, err := domain.NewBilledModelCall(callAt, domain.CallSpeak, "m", 0, time.Second, false); err != nil || !free.Billed() || free.Cost() != 0 {
		t.Fatalf("бесплатный по счету = %+v, %v", free, err)
	}
	for _, c := range []struct {
		name  string
		model string
		cost  int64
		took  time.Duration
	}{
		{"цена меньше нуля", "m", -1, time.Second},
		{"без модели", " ", 1, time.Second},
		{"время меньше нуля", "m", 1, -time.Second},
	} {
		if _, err := domain.NewBilledModelCall(callAt, domain.CallListen, c.model, c.cost, c.took, false); !errors.Is(err, domain.ErrInvalidModelCall) {
			t.Errorf("%s: принят (%v)", c.name, err)
		}
	}
	if plain, _ := domain.NewModelCall(callAt, domain.CallAnswer, "m", 1, 1, true, 0, time.Second, false); plain.Billed() || plain.Cost() != 0 {
		t.Fatalf("обычный вызов со счетом в деньгах: %+v", plain)
	}
}
