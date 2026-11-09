package main

import (
	"os"
	"strings"
	"testing"
)

// Карточка доступа подписывает логин отдельной строкой «логин» (владелец 09.11: «не очень
// понятно, что из этого логин») и дает «Изменить»; форма правки позволяет исправить продукт, среду и логин.
func TestAccessCard_labelsTheLoginAndOffersEdit(t *testing.T) {
	js, err := os.ReadFile("frontend/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	start := strings.Index(src, "function acCardHTML(")
	end := strings.Index(src, "const acProducts")
	card := src[start:end]
	for _, want := range []string{`<dt>логин</dt>`, `data-ac="edit"`} {
		if !strings.Contains(card, want) {
			t.Errorf("в карточке нет %s", want)
		}
	}
	if !strings.Contains(src, "function acEditHTML(") || !strings.Contains(src, `AccessUpdate(`) {
		t.Error("нет формы правки или вызова AccessUpdate")
	}
	i := strings.Index(src, "function acEditHTML(")
	if i < 0 {
		t.Fatal("нет acEditHTML")
	}
	j := strings.Index(src[i:], "\n}\n")
	edit := src[i : i+j]
	for _, want := range []string{`f("product"`, `f("login"`, `name="env"`} {
		if !strings.Contains(edit, want) {
			t.Errorf("в форме правки нельзя исправить поле: нет %s", want)
		}
	}
}

// Цвет плашки логина настраивается в «Оформление → Доступы» (владелец: плашка должна выделяться
// и настраиваться по цветам); стиль берет цвет из переменной, а не жестко.
func TestAccessLogin_colourIsConfigurable(t *testing.T) {
	js, _ := os.ReadFile("frontend/app.js")
	css, _ := os.ReadFile("frontend/style.css")
	if !strings.Contains(string(js), `"login-color"`) || !strings.Contains(string(js), "acLoginApply()") {
		t.Error("нет настройки цвета плашки логина")
	}
	if !strings.Contains(string(css), "var(--ac-login") {
		t.Error("плашка логина не берет цвет из --ac-login")
	}
}
