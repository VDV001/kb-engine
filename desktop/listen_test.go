package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeListen writes a helper with kb-listen's protocol: строки JSON в stdout,
// стоп — закрытый stdin, после него итоговый текст.
func fakeListen(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kb-listen")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

type events struct {
	mu   sync.Mutex
	list []listenEvent
}

func (e *events) add(ev listenEvent) { e.mu.Lock(); e.list = append(e.list, ev); e.mu.Unlock() }
func (e *events) wait(t *testing.T, done func([]listenEvent) bool) []listenEvent {
	t.Helper()
	for range 1000 { // до 10 с: под параллельным just ci помощник стартует медленно
		e.mu.Lock()
		cp := append([]listenEvent(nil), e.list...)
		e.mu.Unlock()
		if done(cp) {
			return cp
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t.Fatalf("не дождался: %+v", e.list)
	return nil
}

// Диктовка: текст идет по мере речи, стоп закрывает stdin, помощник отдает
// итог — окно получает его с final.
func TestDictation_StreamsAndStops(t *testing.T) {
	var ev events
	d := &dictation{bin: fakeListen(t, `echo '{"ready":true,"onDevice":true}'
echo '{"text":"срок"}'
echo '{"text":"срок поставки"}'
cat >/dev/null
echo '{"text":"Срок поставки?","final":true}'
`), emit: ev.add}
	if err := d.Start(0); err != nil {
		t.Fatal(err)
	}
	if err := d.Start(0); err == nil {
		t.Fatal("вторая диктовка поверх первой")
	}
	ev.wait(t, func(l []listenEvent) bool { return len(l) >= 3 })
	d.Stop()
	got := ev.wait(t, func(l []listenEvent) bool { return len(l) > 0 && l[len(l)-1].Final })
	last := got[len(got)-1]
	if !got[0].Ready || !got[0].OnDevice || got[1].Text != "срок" || last.Text != "Срок поставки?" {
		t.Fatalf("events = %+v", got)
	}
	if err := d.Start(0); err != nil { // после конца — можно снова
		t.Fatalf("повторный запуск: %v", err)
	}
	d.Stop()
}

// Помощник отказал (нет разрешения) — окно получает его слова и конец; без
// помощника рядом диктовки нет, а не падение.
func TestDictation_Refusals(t *testing.T) {
	var ev events
	d := &dictation{bin: fakeListen(t, `echo '{"error":"нет разрешения на распознавание речи"}'; exit 3`), emit: ev.add}
	if err := d.Start(0); err != nil {
		t.Fatal(err)
	}
	got := ev.wait(t, func(l []listenEvent) bool { return len(l) > 0 && l[len(l)-1].Final })
	if !strings.Contains(got[0].Error, "разрешения") {
		t.Fatalf("events = %+v", got)
	}
	if err := (&dictation{emit: ev.add}).Start(0); err == nil || !strings.Contains(err.Error(), "собранном окне") {
		t.Fatalf("без помощника: %v", err)
	}
	// Мусор в выводе не роняет окно и не становится текстом.
	var ev2 events
	d2 := &dictation{bin: fakeListen(t, `echo 'not json'; echo '{"text":"ок","final":true}'`), emit: ev2.add}
	if err := d2.Start(0); err != nil {
		t.Fatal(err)
	}
	got = ev2.wait(t, func(l []listenEvent) bool { return len(l) > 0 && l[len(l)-1].Final })
	if got[0].Text != "ок" {
		t.Fatalf("events = %+v", got)
	}
}

// Слушает сервер распознавания: помощник пишет WAV, окно отдает его серверу,
// показывает «распознаю» и итог; файл записи после этого стерт.
func TestDictation_RecordAndTranscribe(t *testing.T) {
	var ev events
	var gotWav []byte
	var path string
	d := &dictation{bin: fakeListen(t, `while [ "$1" != "--record" ]; do shift; done
echo 'RIFF-wav' > "$2"
echo '{"ready":true}'
cat >/dev/null
printf '{"recorded":"%s","final":true}\n' "$2"
`), emit: ev.add, tmpDir: t.TempDir()}
	err := d.StartWith(0, func(_ context.Context, wav []byte) (string, error) {
		gotWav = wav
		return "Срок поставки?", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ev.wait(t, func(l []listenEvent) bool { return len(l) >= 1 })
	d.Stop()
	got := ev.wait(t, func(l []listenEvent) bool { return len(l) > 0 && l[len(l)-1].Final })
	last := got[len(got)-1]
	if strings.TrimSpace(string(gotWav)) != "RIFF-wav" || last.Text != "Срок поставки?" {
		t.Fatalf("wav %q, events %+v", gotWav, got)
	}
	if !slicesContainsBusy(got) {
		t.Fatalf("нет «распознаю»: %+v", got)
	}
	for _, e := range got {
		if e.Recorded != "" {
			path = e.Recorded
		}
	}
	if path != "" {
		t.Fatalf("путь записи ушел странице: %q", path)
	}
	matches, _ := filepath.Glob(filepath.Join(d.tmpDir, "kb-listen-*.wav"))
	if len(matches) != 0 {
		t.Fatalf("запись не стерта: %v", matches)
	}

	// Отказ сервера — словами, конец диктовки.
	var ev2 events
	d2 := &dictation{bin: d.bin, emit: ev2.add, tmpDir: t.TempDir()}
	_ = d2.StartWith(0, func(context.Context, []byte) (string, error) {
		return "", errors.New("сервер распознавания не ответил")
	})
	d2.Stop()
	got = ev2.wait(t, func(l []listenEvent) bool { return len(l) > 0 && l[len(l)-1].Final })
	if !strings.Contains(got[len(got)-1].Error, "не ответил") {
		t.Fatalf("events = %+v", got)
	}
}

func slicesContainsBusy(l []listenEvent) bool {
	for _, e := range l {
		if e.Busy {
			return true
		}
	}
	return false
}
