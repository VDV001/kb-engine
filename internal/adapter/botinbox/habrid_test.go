package botinbox_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/botinbox"
)

// Поле habr_id заполнялось у 36 записей из 717, пришедших из инбокса, — то есть
// не заполнялось никогда, а 36 проставлены руками. Следствие измеримо: проверка
// «нет ли такой статьи в базе» по этому полю смотрела на 5% инбокса и молчала,
// и молчание читалось как «дубликатов нет».
//
// Формы адресов взяты из живого каталога, а не придуманы: 888 записей вида
// /ru/articles/N и 378 корпоративных вида /ru/companies/<блог>/articles/N.
func TestHabrIDFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want int
	}{
		{"https://habr.com/ru/articles/1065834/", 1065834},
		{"https://habr.com/ru/articles/1065834", 1065834},
		{"https://habr.com/ru/companies/otus/articles/1022618/", 1022618},
		{"https://habr.com/ru/companies/cloud4y/articles/999001/", 999001},
		{"https://habr.com/ru/post/123456/", 123456},
		// Спецпроект — тоже материал со своим номером. Форму нашла миграция на
		// живом каталоге: из 1266 адресов один остался неразобранным.
		{"https://habr.com/ru/specials/1034800/", 1034800},
		{"https://habr.com/ru/company/haulmont/blog/654321/", 654321},
		// Чужие адреса номера статьи не несут — и выдумывать его нельзя.
		{"https://example.com/ru/articles/1065834/", 0},
		{"https://github.com/VDV001/kb-engine", 0},
		{"", 0},
		// Мусор после номера не мешает, но сам номер обязан быть номером.
		{"https://habr.com/ru/articles/abc/", 0},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := botinbox.HabrIDFromURL(tc.url); got != tc.want {
				t.Errorf("HabrIDFromURL(%q) = %d, ожидалось %d", tc.url, got, tc.want)
			}
		})
	}
}

// Запись, добавленная из инбокса, обязана нести номер статьи: он есть в адресе,
// и не переносить его — значит терять то, что уже известно.
func TestToEntryParams_carriesTheHabrID(t *testing.T) {
	a := botinbox.Article{
		Title:     "Статья",
		URL:       "https://habr.com/ru/articles/1065834/",
		Hub:       "go",
		CreatedAt: "2026-08-04",
	}
	now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)

	p, err := botinbox.MapArticle(a, now)
	if err != nil {
		t.Fatalf("ToEntryParams: %v", err)
	}
	if p.HabrID == nil {
		t.Fatal("habr_id не проставлен")
	}
	if *p.HabrID != 1065834 {
		t.Errorf("habr_id = %d, ожидалось 1065834", *p.HabrID)
	}
}

// Не-habr источник номера не получает: пустое поле честнее выдуманного.
func TestToEntryParams_leavesForeignSourcesWithoutAnID(t *testing.T) {
	a := botinbox.Article{Title: "Статья", URL: "https://example.com/x/1", Hub: "go"}
	now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)

	p, err := botinbox.MapArticle(a, now)
	if err != nil {
		t.Fatalf("ToEntryParams: %v", err)
	}
	if p.HabrID != nil {
		t.Errorf("выдуман habr_id = %d", *p.HabrID)
	}
}
