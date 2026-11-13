package main

import (
	"context"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// searchModel: в режиме «модель ищет» сначала ищет, потом отвечает.
type searchModel struct{ n int }

func (m *searchModel) Complete(context.Context, []chat.ModelMessage, []chat.Tool) (chat.ModelReply, error) {
	if m.n++; m.n == 1 {
		return chat.ModelReply{ToolCalls: []chat.ToolCall{{ID: "c1", Name: "search", Arguments: `{"q":"срок"}`}}}, nil
	}
	return chat.ModelReply{Content: "ответ"}, nil
}

// Человек видит, куда ходит чат (отзыв 14.11): ссылка из вопроса — выход в
// интернет, поиск — документы окна. Шаги приходят странице по ходу и остаются
// в ответе на диске.
func TestChat_AskShowsWhereItGoes(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	d.model = func(desktopconfig.Chat, string) chat.Model { return &searchModel{} }
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", Mode: desktopconfig.ChatModeTools}, ""); err != nil {
		t.Fatal(err)
	}
	d.attach = func(_ context.Context, ref string) (chat.Doc, error) {
		return chat.Doc{Ref: ref, Title: "ТЗ", Text: "текст"}, nil
	}
	var steps []chat.Use
	d.step = func(u chat.Use) { steps = append(steps, u) }
	c, err := d.Ask(askReq{Question: "что тут? https://example.com/tz"})
	if err != nil {
		t.Fatal(err)
	}
	want := []chat.Use{
		{Kind: chat.UseWeb, Name: "example.com", What: "ссылка из вопроса"},
		{Kind: chat.UseDocs, What: "ищет «срок»"},
	}
	if !slices.Equal(steps, want) {
		t.Errorf("steps\n got %+v\nwant %+v", steps, want)
	}
	saved, err := d.load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Messages[1].Uses; !slices.Equal(got, want) {
		t.Errorf("saved uses %+v", got)
	}
}

// Плашка подключения в ряду «ищет в» выключает кабинет для этого чата: его
// инструменты модель не получает; чат без выбора — все разрешенные.
func TestShopExtra_onlyChosen(t *testing.T) {
	s, _, _ := newServerSettings(t)
	a, _ := s.AddOzon("Брюки", "1", "k-1", true)
	b, _ := s.AddOzon("Платья", "2", "k-2", true)
	for _, tt := range []struct {
		only []string
		want string // кабинет без имени в вопросе; "" — инструментов нет
	}{
		{nil, a.ID},
		{[]string{"folder-1", b.ID}, b.ID},
		{[]string{"folder-1"}, ""},
	} {
		x := shopExtras(s, "", nil, nil, tt.only)
		if tt.want == "" {
			if x != nil {
				t.Errorf("only %v: extras %v", tt.only, x)
			}
			continue
		}
		if u, _ := x[0].Describe("shop_summary", nil); u.ID != tt.want {
			t.Errorf("only %v: cabinet %+v", tt.only, u)
		}
	}
}
