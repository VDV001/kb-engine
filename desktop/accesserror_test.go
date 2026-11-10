package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// «Поменял прод на стенд, сохранить — и ничего не происходит»: все отказы экрана
// «Доступы» (Touch ID, Связка ключей, занятый адрес) шли в showError, а тот писал
// в #settings-error — строку панели настроек, скрытой, пока открыты «Доступы».
// Ошибка должна быть видна там, где человек нажал «Сохранить».
func TestAccessErrorsAreShownInAccessView(t *testing.T) {
	html, err := os.ReadFile("frontend/index.html")
	if err != nil {
		t.Fatal(err)
	}
	section := regexp.MustCompile(`(?s)<section class="access-view" id="access-view"[^>]*>(.*?)</section>`).FindSubmatch(html)
	if section == nil || !strings.Contains(string(section[1]), `id="ac-error"`) {
		t.Fatal(`в разделе «Доступы» нет строки ошибки id="ac-error": ошибки сохранения не видны`)
	}
	js, err := os.ReadFile("frontend/app.js")
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?s)function showError\(msg\) \{(.*?)\n\}`).FindSubmatch(js)
	if fn == nil {
		t.Fatal("нет функции showError")
	}
	if !strings.Contains(string(fn[1]), `#ac-error`) || !strings.Contains(string(fn[1]), `#access-view`) {
		t.Fatal("showError не выбирает строку ошибки «Доступов», когда открыты они")
	}
}
