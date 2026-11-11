package main

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func renameChat(t *testing.T, d *chatDesk) chatRecord {
	t.Helper()
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	c, err := d.Ask(askReq{Question: "первые слова вопроса"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Новое название переживает перезапуск, а история, документы и время
// последнего ответа остаются прежними: переименование не двигает чат в списке.
func TestChat_RenameKeepsEverythingElse(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	c := renameChat(t, d)
	if err := d.RenameChat(c.ID, "  Договор   поставки  "); err != nil {
		t.Fatal(err)
	}
	got, err := newChatDesk(d.settings, d.dir).Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Договор поставки" {
		t.Fatalf("название %q", got.Title)
	}
	if len(got.Messages) != len(c.Messages) || len(got.Docs) != len(c.Docs) || !got.Updated.Equal(c.Updated) {
		t.Fatalf("переименование тронуло чат: %+v -> %+v", c, got)
	}
	list, _ := d.List()
	if len(list) != 1 || list[0].Title != "Договор поставки" {
		t.Fatalf("список %+v", list)
	}
}

// Пустое название и чужой id отказ; длинное название режется как название из вопроса.
func TestChat_RenameRefusals(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	c := renameChat(t, d)
	for name, tc := range map[string]struct{ id, title string }{
		"пустое":       {c.ID, "   "},
		"нет чата":     {"fedcba9876543210", "x"},
		"чужой вид":    {"../x", "x"},
		"пустой id":    {"", "x"},
		"символ строк": {c.ID, "\n\t"},
	} {
		if err := d.RenameChat(tc.id, tc.title); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	if got, _ := d.Get(c.ID); got.Title != "первые слова вопроса" {
		t.Errorf("отказ изменил название: %q", got.Title)
	}
	if err := d.RenameChat(c.ID, strings.Repeat("я", 200)); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Get(c.ID); len([]rune(got.Title)) != 60 {
		t.Errorf("длина названия %d", len([]rune(got.Title)))
	}
}
