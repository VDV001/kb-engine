package main

import (
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
	for range 200 {
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

// Итог диктовки приходит, когда помощник уже освобожден: страница, что сразу
// слушает снова (разговор голосом), не получает «диктовка уже идет» (ревью #465).
func TestDictation_FinalAfterRelease(t *testing.T) {
	var d *dictation
	restarted := make(chan error, 1)
	d = &dictation{bin: fakeListen(t, `echo '{"text":"","final":true}'`), emit: func(ev listenEvent) {
		if ev.Final {
			restarted <- d.Start(0)
		}
	}}
	if err := d.Start(0); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-restarted:
		if err != nil {
			t.Fatalf("повтор после итога: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("итога нет")
	}
	d.Stop()
}
