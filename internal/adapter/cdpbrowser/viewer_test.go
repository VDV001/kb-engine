package cdpbrowser

import (
	"strings"
	"testing"
)

func TestViewer(t *testing.T) {
	page := Viewer(1280, 800)
	// Относительные адреса: окно открывает страницу под /browser/, и абсолютный
	// /frame ушел бы мимо прокси.
	for _, bad := range []string{`"/frame`, `'/frame`, `"/input`, `'/input`, "{{W}}", "{{H}}"} {
		if strings.Contains(page, bad) {
			t.Fatalf("страница содержит %q", bad)
		}
	}
	if !strings.Contains(page, "1280") || !strings.Contains(page, "800") {
		t.Fatal("размер экрана не подставлен")
	}
	// Находки code-review #526: очередь ввода, отпускание кнопки вне холста,
	// сброс номера кадра по смене эпохи.
	for _, want := range []string{"X-Frame-Epoch", "window.addEventListener(\"mouseup\"", "X-Frame-URL"} {
		if !strings.Contains(page, want) {
			t.Errorf("в странице нет %q", want)
		}
	}
	// Адрес без схемы получает https://, но «localhost:3000» схемой не считается.
	if !strings.Contains(page, `:\/\/`) {
		t.Error("проверка схемы адреса не по «://»")
	}
}
