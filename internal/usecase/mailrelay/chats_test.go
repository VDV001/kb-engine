package mailrelay_test

import (
	"context"
	"testing"

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
// доходят до чата окна; удаленный чат забывается — ответы туда не пойдут;
// чат, которого окно еще не знает, — подтверждение без правки.
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
	if _, ok := s.st.Chats["c-1"]; ok {
		t.Error("a deleted chat is still mapped: its next answer would reach nowhere")
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
