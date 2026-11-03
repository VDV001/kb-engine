//go:build !(darwin && desktop)

package main

// Без тега desktop (go test, режим -browser на чужой ОС) меню-бара нет: тесты
// не тянут Cocoa, а логику переключения проверяет tray_test.go.

func installMenuBar(tray) {}
