//go:build darwin && desktop

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework Carbon
void kbInstallMenuBar(void);
*/
import "C"

import (
	"log"
	"sync/atomic"
)

// menuBar is the tray the native code calls back into. atomic: пишется в
// OnStartup, читается из горутин колбэков Cocoa (ревью #380).
var menuBar atomic.Pointer[tray]

// installMenuBar puts the icon in the menu bar and registers ⌥Space
// (Carbon RegisterEventHotKey: разрешения «Универсальный доступ» не нужно).
func installMenuBar(t tray) {
	menuBar.Store(&t)
	C.kbInstallMenuBar()
}

// Вызовы приходят с главного потока Cocoa; работа уходит в горутину, чтобы
// рантайм Wails, который сам ходит на главный поток, его не ждал. Контекст
// Wails проверяет tray.

//export kbToggle
func kbToggle(frontmost C.int) {
	if t := menuBar.Load(); t != nil {
		go t.summon(frontmost != 0)
	}
}

//export kbQuit
func kbQuit() {
	if t := menuBar.Load(); t != nil {
		go t.quit()
	}
}

//export kbHotkeyFailed
func kbHotkeyFailed(status C.int) {
	log.Printf("menubar: ⌥Space не зарегистрирована (OSStatus %d): ее заняло другое приложение; окно открывается из меню-бара", int(status))
}
