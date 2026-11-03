package main

import (
	"context"
	"slices"
	"testing"
)

// fakeWindow записывает, что меню-бар и горячая клавиша делали с окном.
type fakeWindow struct{ calls []string }

func (f *fakeWindow) Show() { f.calls = append(f.calls, "show") }
func (f *fakeWindow) Hide() { f.calls = append(f.calls, "hide") }
func (f *fakeWindow) Quit() { f.calls = append(f.calls, "quit") }

// ⌥Space работает переключателем: окно на переднем плане прячется, иначе
// появляется — в том числе после красной кнопки и Esc, которые его только
// прячут. Пункт меню «Открыть поиск» всегда показывает.
func TestTray_Summon(t *testing.T) {
	tests := []struct {
		name      string
		frontmost bool
		want      []string
	}{
		{"окно спрятано или за другими", false, []string{"show"}},
		{"окно на переднем плане", true, []string{"hide"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &fakeWindow{}
			tray{win: w, ctx: context.Background()}.summon(tt.frontmost)
			if !slices.Equal(w.calls, tt.want) {
				t.Fatalf("calls = %v, want %v", w.calls, tt.want)
			}
		})
	}
}

func TestTray_Quit(t *testing.T) {
	w := &fakeWindow{}
	tray{win: w, ctx: context.Background()}.quit()
	if !slices.Equal(w.calls, []string{"quit"}) {
		t.Fatalf("calls = %v", w.calls)
	}
}

// ⌥Space, нажатая во время выхода, окно не трогает: колбэк Cocoa приходит в
// горутину, а контекст Wails уже закрыт (ревью #380).
func TestTray_IgnoresCallsAfterShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &fakeWindow{}
	tr := tray{win: w, ctx: ctx}
	tr.summon(false)
	tr.summon(true)
	tr.quit()
	if len(w.calls) != 0 {
		t.Fatalf("calls = %v, ждали ни одного", w.calls)
	}
}
