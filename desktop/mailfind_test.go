package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// Поиск телефона — тот же поиск, что у чата окна; открыть можно только
// найденное этим поиском: ref, придуманный телефоном, — отказ.
func TestPhoneFinder(t *testing.T) {
	long := strings.Repeat("слово ", 100)
	var limit int
	f := newPhoneFinder(
		func(_ context.Context, q string, n int) ([]chat.Doc, error) {
			limit = n
			return []chat.Doc{
				{Ref: "file:/Users/a/Договоры/Альфа.PDF", Title: "Альфа.PDF", Source: "/Users/a/Договоры", Text: "Поставщик\nобязуется " + long},
				{Ref: "kb:42", Title: "ООО «Альфа»: оплата", Source: "База знаний", Text: "отсрочка 30 дней"},
			}, nil
		},
		func(_ context.Context, doc chat.Doc) (chat.Doc, error) {
			return chat.Doc{Ref: doc.Ref, Title: "Альфа.PDF", Text: "полный текст"}, nil
		},
	)
	hits, err := f.Search(t.Context(), "альфа")
	if err != nil {
		t.Fatal(err)
	}
	if limit != phoneHits || len(hits) != 2 {
		t.Fatalf("предел %d, выдача %d", limit, len(hits))
	}
	h := hits[0]
	if h.Kind != "pdf" || h.Where != "Договоры" || strings.Contains(h.Snippet, "\n") || utf8.RuneCountInString(h.Snippet) > phoneSnippet+1 {
		t.Fatalf("файл: %+v", h)
	}
	if hits[1].Kind != "запись" || hits[1].Where != "База знаний" {
		t.Fatalf("запись: %+v", hits[1])
	}
	doc, err := f.Open(t.Context(), h.Ref)
	if err != nil || doc.Text != "полный текст" || doc.Title != "Альфа.PDF" {
		t.Fatalf("открыто %+v, %v", doc, err)
	}
	if _, err := f.Open(t.Context(), "file:/etc/passwd"); err == nil || !strings.Contains(err.Error(), "найдите") {
		t.Fatalf("ref не из выдачи открыт: %v", err)
	}
}

// Показанное забывается с самого старого, а не все сразу: выдача, которая
// висит на телефоне, открывается и после десятков поисков (ревью #636).
func TestPhoneFinderForgetsOldestFirst(t *testing.T) {
	n := 0
	f := newPhoneFinder(
		func(context.Context, string, int) ([]chat.Doc, error) {
			docs := make([]chat.Doc, phoneHits)
			for i := range docs {
				n++
				docs[i] = chat.Doc{Ref: fmt.Sprintf("kb:%d", n), Title: "запись"}
			}
			return docs, nil
		},
		func(_ context.Context, doc chat.Doc) (chat.Doc, error) { return doc, nil },
	)
	for range 30 {
		if _, err := f.Search(t.Context(), "q"); err != nil {
			t.Fatal(err)
		}
	}
	recent := fmt.Sprintf("kb:%d", 30*phoneHits-phoneShown+1) // самая старая из помнимых
	if _, err := f.Open(t.Context(), recent); err != nil {
		t.Fatalf("недавняя выдача не открылась: %v", err)
	}
	if _, err := f.Open(t.Context(), "kb:1"); !errors.Is(err, errNotShown) {
		t.Fatalf("самая старая выдача: %v, want errNotShown", err)
	}
}

// Отказы телефону — словами окна: без поиска и по сроку relay (ревью #636).
func TestPhoneFinderWords(t *testing.T) {
	if _, err := (&mailWorker{}).finder().Search(t.Context(), "q"); !errors.Is(err, errPhoneSearchOff) {
		t.Fatalf("окно без поиска: %v", err)
	}
	f := newPhoneFinder(func(ctx context.Context, _ string, _ int) ([]chat.Doc, error) { return nil, ctx.Err() }, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	if _, err := f.Search(ctx, "q"); !errors.Is(err, errPhoneSlow) {
		t.Fatalf("по сроку: %v", err)
	}
}

// Метки «взято» — из последнего ответа чата: подключения и документы, без повторов.
func TestAnswerSources(t *testing.T) {
	rec := chatRecord{
		Docs: []chatDoc{{Ref: "file:/a/отчет.xlsx", Title: "отчет.xlsx"}, {Ref: "kb:1", Title: "Старое"}},
		Messages: []chatMessage{
			{Role: "user", Text: "заказы"},
			{Role: "assistant", Text: "312", Docs: []string{"file:/a/отчет.xlsx", "file:/a/отчет.xlsx"},
				Uses: []chat.Use{{Kind: chat.UseConnection, Name: "Ozon · Пример"}}},
		},
	}
	got := answerSources(rec)
	want := []mailrelay.Source{{Kind: string(chat.UseConnection), Name: "Ozon · Пример"}, {Kind: "doc", Name: "отчет.xlsx"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("метки %+v", got)
	}
}
