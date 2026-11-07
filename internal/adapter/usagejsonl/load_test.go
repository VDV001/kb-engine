package usagejsonl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/usagejsonl"
	"github.com/daniil/kb-engine/internal/domain"
)

// Журнал читается обратно для экрана «Статистика»: записанное — то же
// прочитанное; файла нет — пусто, а не ошибка; битая строка (обрыв записи,
// правка руками) пропускается и считается, а не роняет весь экран.
func TestJournal_Load(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	got, bad, err := j.Load()
	if err != nil || len(got) != 0 || bad != 0 {
		t.Fatalf("нет файла: %v %d %v", got, bad, err)
	}
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	a, _ := domain.NewModelCall(at, domain.CallSearch, "anthropic/claude-sonnet-5.5", 3100, 180, true, 900*time.Millisecond, 4*time.Second, false)
	b, _ := domain.NewModelCall(at.Add(time.Minute), domain.CallAnswer, "qwen3", 0, 0, false, 2*time.Second, 2*time.Second, true)
	for _, c := range []domain.ModelCall{a, b} {
		if err := j.Record(c); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{обрыв\n" + `{"at":"2026-11-07T12:00:00Z","purpose":"chat","model":"m","in":1,"out":1,"first_ms":0,"took_ms":1}` + "\n")
	_ = f.Close()
	got, bad, err = j.Load()
	if err != nil || len(got) != 2 || bad != 2 {
		t.Fatalf("прочитано %d, битых %d, %v", len(got), bad, err)
	}
	if g := got[0]; g.Purpose() != domain.CallSearch || g.In() != 3100 || !g.Counted() || g.FirstToken() != 900*time.Millisecond || !g.At().Equal(at) {
		t.Fatalf("первый = %+v", g)
	}
	if g := got[1]; g.Counted() || !g.Failed() || g.Model() != "qwen3" {
		t.Fatalf("второй = %+v", g)
	}
}

// Строка длиннее буфера сканера (64 КБ) — одна битая строка, а не ошибка
// чтения всего журнала: остальные вызовы видны, экран не падает.
func TestJournal_Load_longLineIsOneBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c, _ := domain.NewModelCall(at, domain.CallAnswer, "m", 1, 1, true, 0, time.Second, false)
	if err := j.Record(c); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"model":"` + strings.Repeat("x", 200<<10) + "\"}\n")
	_ = f.Close()
	if err := j.Record(c); err != nil {
		t.Fatal(err)
	}
	got, bad, err := j.Load()
	if err != nil || len(got) != 2 || bad != 1 {
		t.Fatalf("прочитано %d, битых %d, %v", len(got), bad, err)
	}
}

// Чтение ждет замка записи: иначе недописанная строка читается как битая, и
// экран показывает ложное «нечитаемых: 1».
func TestJournal_Load_waitsForWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	j := usagejsonl.Journal{Path: path}
	c, _ := domain.NewModelCall(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), domain.CallAnswer, "m", 1, 1, true, 0, time.Second, false)
	if err := j.Record(c); err != nil {
		t.Fatal(err)
	}
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		_ = filelock.With(path, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	go func() {
		_, _, _ = j.Load()
		close(done)
	}()
	select {
	case <-done:
		close(release)
		t.Fatal("Load прочитал журнал под чужим замком")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Load не дождался замка")
	}
}
