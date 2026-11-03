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
// Dock (LSUIElement), красная кнопка и Esc его только прячут, а индекс и
// распознавание продолжают работать в фоне.
type tray struct{ win window }

// summon toggles the window: на переднем плане — прячет, иначе показывает.
func (t tray) summon(frontmost bool) {
	if frontmost {
		t.win.Hide()
		return
	}
	t.win.Show()
}

func (t tray) quit() { t.win.Quit() }

// wailsWindow drives the Wails window. ctx — контекст из OnStartup: до него
// рантайма Wails еще нет.
type wailsWindow struct{ ctx func() context.Context }

// Show brings the window forward and puts the cursor in the search field.
func (w wailsWindow) Show() {
	ctx := w.ctx()
	runtime.WindowShow(ctx)
	activateApp() // приложению без Dock фокус сам не приходит
	runtime.EventsEmit(ctx, "kb:show")
}

func (w wailsWindow) Hide() { runtime.WindowHide(w.ctx()) }
func (w wailsWindow) Quit() { runtime.Quit(w.ctx()) }
