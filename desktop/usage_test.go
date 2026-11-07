package main

import (
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
