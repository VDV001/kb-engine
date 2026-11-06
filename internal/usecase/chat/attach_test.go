package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

var attached = chat.Doc{Ref: "url:https://example.com/tz", Title: "ТЗ по ссылке", Source: "example.com", Text: "Срок сдачи — 15 декабря"}

// Приложенный документ (файл, найденное, ссылка) идет модели первым, под
// номером [1], и остается в документах чата, даже если модель на него не
// сослалась; найденное поиском добирает остальные места без повторов.
func TestRespond_ContextModeAttachedFirst(t *testing.T) {
	s := &fakeSearch{docs: append([]chat.Doc{attached}, docs...)}
	m := &fakeModel{reply: chat.ModelReply{Content: "Срок — 30 дней [2]."}}
	turn, err := chat.NewService(s, m).Respond(context.Background(), chat.Request{
		Question: "какой срок?", Mode: chat.ModeContext, Attached: []chat.Doc{attached},
	})
	if err != nil {
		t.Fatal(err)
	}
	user := m.got[len(m.got)-1].Content
	if !strings.Contains(user, "[1] ТЗ по ссылке") || !strings.Contains(user, "[2] Договор поставки") || strings.Count(user, "ТЗ по ссылке") != 1 {
		t.Fatalf("запрос модели:\n%s", user)
	}
	if len(turn.Docs) != 2 || turn.Docs[0].Ref != attached.Ref || turn.Docs[1].Ref != "kb:12" {
		t.Fatalf("docs = %+v", turn.Docs)
	}
}

// В режиме «модель ищет» приложенное лежит в вопросе целиком, и модель может
// ответить, ничего не ища; оно же — прочитанный документ хода.
func TestRespond_ToolsModeAttachedInQuestion(t *testing.T) {
	m := &scriptModel{replies: []chat.ModelReply{{Content: "15 декабря."}}}
	turn, err := chat.NewService(&fakeSearch{}, m, chat.WithReader(&fakeReader{})).Respond(context.Background(), chat.Request{
		Question: "когда сдавать?", Mode: chat.ModeTools, Attached: []chat.Doc{attached},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := m.asked[0][len(m.asked[0])-1].Content
	if !strings.Contains(q, "Срок сдачи — 15 декабря") || !strings.Contains(q, "когда сдавать?") {
		t.Fatalf("вопрос модели:\n%s", q)
	}
	if len(turn.Docs) != 1 || turn.Docs[0].Ref != attached.Ref {
		t.Fatalf("docs = %+v", turn.Docs)
	}
}

// Приложенный текст режется по своему потолку, больше, чем у найденного:
// человек приложил документ, чтобы модель его прочла.
func TestRespond_AttachedTrimmed(t *testing.T) {
	long := attached
	long.Text = strings.Repeat("я", chat.MaxAttachedRunes+100)
	m := &fakeModel{reply: chat.ModelReply{Content: "ок"}}
	if _, err := chat.NewService(&fakeSearch{}, m).Respond(context.Background(), chat.Request{
		Question: "?", Mode: chat.ModeContext, Attached: []chat.Doc{long},
	}); err != nil {
		t.Fatal(err)
	}
	user := m.got[len(m.got)-1].Content
	if n := strings.Count(user, "я"); n != chat.MaxAttachedRunes {
		t.Fatalf("приложенного ушло %d знаков, ждали %d", n, chat.MaxAttachedRunes)
	}
}

// В режиме «модель ищет» найденное поиском остается в документах хода и при
// приложенном: приложенное — к ним, а не вместо них (ревью #464).
func TestRespond_ToolsModeKeepsFoundWithAttached(t *testing.T) {
	m := &scriptModel{replies: []chat.ModelReply{call("c1", "search", `{"q":"срок"}`), {Content: "Срок 30 дней."}}}
	turn, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{})).Respond(context.Background(), chat.Request{
		Question: "срок?", Mode: chat.ModeTools, Attached: []chat.Doc{attached},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Docs) != 3 || turn.Docs[0].Ref != attached.Ref || turn.Docs[1].Ref != "kb:12" {
		t.Fatalf("docs = %+v", turn.Docs)
	}
}
