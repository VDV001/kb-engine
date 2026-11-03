package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// window — то, что меню-бару и горячей клавише нужно от окна.
type window interface {
	Show()
	Hide()
	Quit()
}

// tray is the menu-bar icon and the ⌥Space hotkey: окно живет без значка в
// Dock, красная кнопка и Esc его только прячут, а индекс и распознавание
// продолжают работать в фоне. ctx — контекст Wails: после выхода колбэки
// Cocoa окно не трогают (ревью #380).
type tray struct {
	win window
	ctx context.Context
}

// summon toggles the window: на переднем плане — прячет, иначе показывает.
func (t tray) summon(frontmost bool) {
	switch {
	case t.ctx.Err() != nil:
	case frontmost:
		t.win.Hide()
	default:
		t.win.Show()
	}
}

func (t tray) quit() {
	if t.ctx.Err() == nil {
		t.win.Quit()
	}
}

// wailsWindow drives the Wails window. Прячется и показывается приложение
// целиком, как у красной кнопки ([NSApp hide:]): фокус возвращается в
// приложение, из которого звали, а оба пути прятать ведут себя одинаково
// (ревью #380: WindowHide оставлял kb активным без окна).
type wailsWindow struct{ ctx context.Context }

// Show unhides the app, brings the window forward and puts the cursor in the
// search field.
func (w wailsWindow) Show() {
	runtime.Show(w.ctx)
	runtime.WindowShow(w.ctx)
	runtime.EventsEmit(w.ctx, "kb:show")
}

func (w wailsWindow) Hide() { runtime.Hide(w.ctx) }
func (w wailsWindow) Quit() { runtime.Quit(w.ctx) }
