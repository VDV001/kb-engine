//go:build darwin && desktop

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework Carbon
void kbInstallMenuBar(void);
void kbActivate(void);
*/
import "C"

import "log"

// menuBar is the tray the native code calls back into. Ставится один раз в
// OnStartup, до того как значок и клавиша зарегистрированы.
var menuBar tray

// installMenuBar puts the icon in the menu bar and registers ⌥Space
// (Carbon RegisterEventHotKey: разрешения «Универсальный доступ» не нужно).
func installMenuBar(t tray) {
	menuBar = t
	C.kbInstallMenuBar()
}

func activateApp() { C.kbActivate() }

// Вызовы приходят с главного потока Cocoa; работа уходит в горутину, чтобы
// рантайм Wails, который сам ходит на главный поток, его не ждал.

//export kbToggle
func kbToggle(frontmost C.int) { go menuBar.summon(frontmost != 0) }

//export kbQuit
func kbQuit() { go menuBar.quit() }

//export kbHotkeyFailed
func kbHotkeyFailed(status C.int) {
	log.Printf("menubar: ⌥Space не зарегистрирована (OSStatus %d): ее заняло другое приложение; окно открывается из меню-бара", int(status))
}
