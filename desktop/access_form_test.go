package main

import (
	"os"
	"strings"
	"testing"
)

// Кнопки формы «Добавить доступ» называются своим классом: у общего `.acts` в стилях
// стоит opacity: 0 (строки источников), и «Сохранить» с «Отменой» не были видны вовсе.
// Тестового стенда для app.js нет, поэтому коллизию держит этот текстовый чек.
func TestAccessForm_buttonsDoNotShareTheSourceRowsClass(t *testing.T) {
	js, err := os.ReadFile("frontend/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	start := strings.Index(src, "function acAddHTML()")
	end := strings.Index(src, "function acInsideHTML()")
	if start < 0 || end < start {
		t.Fatal("в app.js нет acAddHTML перед acInsideHTML")
	}
	form := src[start:end]
	if strings.Contains(form, `class="acts"`) {
		t.Error(`форма доступа использует общий класс "acts" (у него opacity: 0 для строк источников)`)
	}
	if !strings.Contains(form, `class="ac-acts"`) {
		t.Error(`у кнопок формы доступа нет своего класса "ac-acts"`)
	}

	css, err := os.ReadFile("frontend/style.css")
	if err != nil {
		t.Fatal(err)
	}
	var rule string
	for l := range strings.SplitSeq(string(css), "\n") {
		if strings.HasPrefix(l, ".ac-acts {") {
			rule = l
		}
	}
	if rule == "" {
		t.Fatal("в style.css нет правила .ac-acts")
	}
	if strings.Contains(rule, "opacity: 0") {
		t.Errorf("кнопки формы скрыты: %s", rule)
	}
}
