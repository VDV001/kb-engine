package mailrelay_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// chatEditor records what the phone did to the window's chats.
type chatEditor struct{ renamed, deleted []string }

func (e *chatEditor) RenameChat(_ context.Context, id, title string) error {
	e.renamed = append(e.renamed, id+"="+title)
	return nil
}

func (e *chatEditor) DeleteChat(_ context.Context, id string) error {
	e.deleted = append(e.deleted, id)
	return nil
}

// Один чат в двух местах (#649): переименование и удаление с телефона
// доходят до чата окна; чат, которого окно еще не знает, — подтверждение.
// Связь удаленного чата живет до заметки окна об удалении: она дойдет до
// всех телефонов ключа (ревью #662).
func TestRelayEditsTheWindowsChat(t *testing.T) {
	b, e, s := &box{}, &chatEditor{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	b.phone("c-1", "A", 2, mailrelay.Payload{Kind: mailrelay.KindRename, Text: "  Поставки  "})
	b.phone("c-1", "A", 3, mailrelay.Payload{Kind: mailrelay.KindDelete, Text: "chat"})
	b.phone("c-9", "A", 1, mailrelay.Payload{Kind: mailrelay.KindRename, Text: "новый"})
	r := newRelay(b, &asker{}, s)
	r.Chats = e
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(e.renamed) != 1 || e.renamed[0] != "w-1=Поставки" || len(e.deleted) != 1 || e.deleted[0] != "w-1" {
		t.Fatalf("renamed %v, deleted %v", e.renamed, e.deleted)
	}
	got := payloads(t, b)
	if len(got) != 4 || got[1].Kind != mailrelay.KindEdited || got[2].Kind != mailrelay.KindEdited || got[3].Kind != mailrelay.KindEdited {
		t.Fatalf("answers %+v", got)
	}
}

// Из окна — на телефон (#649): переименование и удаление чата окна уходят в
// связанный чат телефона; после удаления связь забыта.
func TestRelayTellsThePhoneTheWindowsEdit(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	r := newRelay(b, &asker{}, s)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.Notes = []mailrelay.Note{
		{Chat: "w-1", Payload: mailrelay.Payload{Kind: mailrelay.KindRename, Text: "Поставки"}},
		{Chat: "w-1", Payload: mailrelay.Payload{Kind: mailrelay.KindDelete, Text: "chat"}},
	}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if len(got) != 3 || got[1].Kind != mailrelay.KindRename || got[1].Text != "Поставки" || got[2].Kind != mailrelay.KindDelete {
		t.Fatalf("phone got %+v", got)
	}
	if _, ok := s.st.Chats["c-1"]; ok {
		t.Error("the window deleted the chat, the mapping stays")
	}
}

// Удаление с телефона A доходит до телефона B (ревью #662): связь не
// забывается до заметки окна, заметка уходит в чат ящика и забывает связь и
// предложенные факты.
func TestRelayPhoneDeleteReachesOtherPhones(t *testing.T) {
	b, e, s := &box{}, &chatEditor{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	r := newRelay(b, &asker{}, s)
	r.Chats = e
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.st.Offered = map[string][]string{"c-1": {"f-1"}}
	b.phone("c-1", "A", 2, mailrelay.Payload{Kind: mailrelay.KindDelete, Text: "chat"})
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.st.Chats["c-1"] != "w-1" {
		t.Fatal("связь забыта до заметки окна: телефон B удаления не получит")
	}
	r.Notes = []mailrelay.Note{{Chat: "w-1", Payload: mailrelay.Payload{Kind: mailrelay.KindDelete, Text: "chat"}}}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if last := got[len(got)-1]; last.Kind != mailrelay.KindDelete {
		t.Fatalf("телефоны получили %+v", got)
	}
	if _, ok := s.st.Chats["c-1"]; ok {
		t.Error("после заметки об удалении связь осталась")
	}
	if _, ok := s.st.Offered["c-1"]; ok {
		t.Error("предложенные факты удаленного чата остались в состоянии")
	}
}

// Переименование чата, которого окно еще не знает (ревью #662): название
// ждет первого хода и ложится на новый чат окна.
func TestRelayRenameBeforeTheWindowKnowsTheChat(t *testing.T) {
	b, e, s := &box{}, &chatEditor{}, &store{}
	b.phone("c-9", "A", 1, mailrelay.Payload{Kind: mailrelay.KindRename, Text: "Поставки"})
	r := newRelay(b, &asker{}, s)
	r.Chats = e
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	b.phone("c-9", "A", 2, q("сроки?"))
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(e.renamed) != 1 || e.renamed[0] != "w-1=Поставки" {
		t.Fatalf("renamed %v", e.renamed)
	}
}

// Предел названия — как у окна, 60 знаков (ревью #662): длиннее телефон не
// обещает, окно не режет второй раз.
func TestRelayRenameKeepsTheWindowsLimit(t *testing.T) {
	b, e, s := &box{}, &chatEditor{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	b.phone("c-1", "A", 2, mailrelay.Payload{Kind: mailrelay.KindRename, Text: strings.Repeat("я", 70)})
	r := newRelay(b, &asker{}, s)
	r.Chats = e
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(e.renamed) != 1 || utf8.RuneCountInString(strings.TrimPrefix(e.renamed[0], "w-1=")) != 60 {
		t.Fatalf("renamed %v", e.renamed)
	}
}
