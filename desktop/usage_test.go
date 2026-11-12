package main

import (
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
)

type callsRec struct {
	mu    sync.Mutex
	calls []domain.ModelCall
}

func (r *callsRec) Record(c domain.ModelCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	return nil
}

func (r *callsRec) Load() ([]domain.ModelCall, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls), 0, nil
}

func (r *callsRec) purposes() []domain.CallPurpose {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.CallPurpose{}
	for _, c := range r.calls {
		out = append(out, c.Purpose())
	}
	return out
}

// Окно считает каждый вызов модели для статистики: ответ в чате и разбор
// ответа для портрета — каждый со своим назначением и моделью из настроек.
func TestChat_RecordsModelCalls(t *testing.T) {
	d, _, news := newPortraitDesk(t)
	rec := &callsRec{}
	d.calls = rec
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryDir: t.TempDir()}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ask(askReq{Question: "Как настроить голос?"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-news:
	case <-time.After(3 * time.Second):
		t.Fatal("разбор после ответа не пришел")
	}
	got := rec.purposes()
	if !slices.Contains(got, domain.CallAnswer) || !slices.Contains(got, domain.CallExtract) {
		t.Fatalf("назначения = %v", got)
	}
	rec.mu.Lock()
	calls := slices.Clone(rec.calls)
	rec.mu.Unlock()
	for _, c := range calls {
		if c.Model() != "qwen3" {
			t.Fatalf("модель = %q", c.Model())
		}
	}
}

// Экран «Статистика»: сводка журнала за период с подписями для человека —
// назначения по-русски, модель как в настройках.
func TestChat_Stats(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	rec := &callsRec{}
	d.calls = rec
	now := d.now()
	a, _ := domain.NewModelCall(now.Add(-time.Hour), domain.CallSearch, "anthropic/claude-sonnet-5.5", 3100, 180, true, time.Second, 4*time.Second, false)
	b, _ := domain.NewModelCall(now.Add(-2*time.Hour), domain.CallExtract, "anthropic/claude-sonnet-5.5", 900, 60, true, time.Second, time.Second, false)
	rec.calls = []domain.ModelCall{a, b}
	v, err := d.Stats(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Days) != 7 || v.Total.Calls != 2 || v.Total.In != 4000 || len(v.Calls) != 2 {
		t.Fatalf("сводка = %+v", v)
	}
	titles := map[string]string{}
	for _, p := range v.Purposes {
		titles[p.ID] = p.Title
	}
	if titles["search"] != "Поиск моделью" || titles["extract"] != "Разбор портрета" {
		t.Fatalf("подписи = %v", titles)
	}
	if c := v.Calls[0]; c.Purpose != "search" || c.Title != "Поиск моделью" || c.In != 3100 || c.TookMS != 4000 || !c.Counted {
		t.Fatalf("вызов = %+v", c)
	}
	if _, err := d.Stats(0); err == nil {
		t.Fatal("период 0 дней принят")
	}
}

// Журнал фильтрует вызовы по чату (#541): вызов несет id чата, а названия
// чатов приходят одной картой, по одному чтению на чат; удаленный чат —
// общим названием, как в «Деньгах».
func TestChat_Stats_callsCarryChat(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryOff: true}, ""); err != nil {
		t.Fatal(err)
	}
	a, err := d.Ask(askReq{Question: "первый чат"})
	if err != nil {
		t.Fatal(err)
	}
	rec := &callsRec{}
	d.calls = rec
	now := d.now()
	in, _ := domain.NewModelCall(now.Add(-time.Hour), domain.CallAnswer, "qwen3", 100, 10, true, time.Second, time.Second, false)
	gone, _ := domain.NewModelCall(now.Add(-2*time.Hour), domain.CallAnswer, "qwen3", 100, 10, true, time.Second, time.Second, false)
	out, _ := domain.NewModelCall(now.Add(-3*time.Hour), domain.CallExtract, "qwen3", 100, 10, true, time.Second, time.Second, false)
	rec.calls = []domain.ModelCall{in.WithChat(a.ID), gone.WithChat("0123456789abcdef"), out}
	v, err := d.Stats(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Calls) != 3 || v.Calls[0].Chat != a.ID || v.Calls[1].Chat != "0123456789abcdef" || v.Calls[2].Chat != "" {
		t.Fatalf("чаты вызовов = %+v", v.Calls)
	}
	want := map[string]string{a.ID: "первый чат", "0123456789abcdef": "Удаленный чат"}
	if !maps.Equal(v.Chats, want) {
		t.Fatalf("названия чатов = %v, want %v", v.Chats, want)
	}
}

// Вызов без счета отдается окну без токенов: Журнал складывает строки сам, и
// числа из строки «без счета» (правка журнала руками) попали бы в его сумму.
func TestChat_Stats_uncountedCallHasNoTokens(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	rec := &callsRec{}
	d.calls = rec
	c, _ := domain.NewModelCall(d.now().Add(-time.Hour), domain.CallAnswer, "qwen3", 7000, 700, false, time.Second, time.Second, false)
	rec.calls = []domain.ModelCall{c}
	v, err := d.Stats(7)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Calls[0]; got.Counted || got.In != 0 || got.Out != 0 {
		t.Fatalf("вызов без счета = %+v", got)
	}
}
