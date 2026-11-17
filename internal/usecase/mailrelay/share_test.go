package mailrelay_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// deskPosts is what the window posted, with the mailbox chat.
func deskPosts(t *testing.T, b *box) (chats []string, ps []mailrelay.Payload) {
	t.Helper()
	for _, m := range b.msgs {
		if m.From != domain.MailDesk {
			continue
		}
		var s fakeSealed
		var p mailrelay.Payload
		if json.Unmarshal(m.Sealed, &s) != nil || json.Unmarshal(s.Plain, &p) != nil {
			t.Fatalf("garbage %s", m.Sealed)
		}
		chats, ps = append(chats, m.Chat), append(ps, p)
	}
	return chats, ps
}

func exchanges(n int) []mailrelay.Exchange {
	var out []mailrelay.Exchange
	for i := range n {
		out = append(out, mailrelay.Exchange{Question: fmt.Sprintf("вопрос %d", i+1), Answer: fmt.Sprintf("ответ %d", i+1)})
	}
	return out
}

// Новый чат окна (#669) приходит на телефон целиком: название, все ходы по
// порядку; дальше — только новый ход.
func TestRelaySharesANewWindowChat(t *testing.T) {
	b, s := &box{}, &store{}
	r := newRelay(b, &asker{}, s)
	r.Turns = []mailrelay.WindowTurn{{Chat: "w-7", Title: "Поставки", New: true, History: exchanges(2)}}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	chats, ps := deskPosts(t, b)
	if len(ps) != 4 || ps[0].Kind != mailrelay.KindAsked || ps[0].Text != "вопрос 1" || ps[0].Title != "Поставки" ||
		ps[1].Kind != mailrelay.KindAnswer || ps[1].Text != "ответ 1" || ps[2].Text != "вопрос 2" || ps[3].Text != "ответ 2" {
		t.Fatalf("posted %+v", ps)
	}
	mc := chats[0]
	if err := domain.ValidateMailChat(mc); err != nil || s.st.Chats[mc] != "w-7" || chats[3] != mc {
		t.Fatalf("chat %q (%v), mapping %v", mc, err, s.st.Chats)
	}
	r.Turns = []mailrelay.WindowTurn{{Chat: "w-7", Title: "Поставки", New: true, History: exchanges(3)}}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	chats, ps = deskPosts(t, b)
	if len(ps) != 6 || ps[4].Text != "вопрос 3" || ps[5].Text != "ответ 3" || chats[5] != mc {
		t.Fatalf("second turn %+v", ps[4:])
	}
}

// Ход окна в чате телефона виден на телефоне; старый чат окна (до выкатки) —
// не уходит.
func TestRelaySharesWindowTurnsOfAPhoneChat(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	r := newRelay(b, &asker{}, s)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.Turns = []mailrelay.WindowTurn{
		{Chat: "w-1", Title: "привет", History: exchanges(2)},
		{Chat: "w-old", Title: "старый", History: exchanges(1)},
	}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	chats, ps := deskPosts(t, b)
	if len(ps) != 3 || ps[1].Kind != mailrelay.KindAsked || ps[1].Text != "вопрос 2" || ps[2].Text != "ответ 2" || chats[1] != "c-1" {
		t.Fatalf("posted %v %+v", chats, ps)
	}
	if len(s.st.Shared) != 0 {
		t.Errorf("phone chat counted in the window's 50: %v", s.st.Shared)
	}
}

// Лимит ящика (решение владельца 17.11): 50 последних чатов окна; самый
// старый уходит с телефонов удалением, в окне остается.
func TestRelayKeepsTheLast50WindowChats(t *testing.T) {
	b, s := &box{}, &store{}
	r := newRelay(b, &asker{}, s)
	for i := range mailrelay.MaxSharedChats + 1 {
		r.Turns = []mailrelay.WindowTurn{{Chat: fmt.Sprintf("w-%d", i), Title: "т", New: true, History: exchanges(1)}}
		if err := r.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.st.Shared) != mailrelay.MaxSharedChats {
		t.Fatalf("shared %d", len(s.st.Shared))
	}
	chats, ps := deskPosts(t, b)
	last := ps[len(ps)-1]
	if last.Kind != mailrelay.KindDelete || chats[len(chats)-1] != chats[0] {
		t.Fatalf("the oldest is not deleted on the phone: %+v in %s", last, chats[len(chats)-1])
	}
	for mc, wc := range s.st.Chats {
		if wc == "w-0" {
			t.Errorf("w-0 still linked as %s", mc)
		}
	}
	// Ход в чате, выпавшем с телефона, возвращает его целиком и освежает место.
	r.Turns = []mailrelay.WindowTurn{{Chat: "w-0", Title: "т", New: true, History: exchanges(2)}}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ps2 := deskPosts(t, b)
	if got := ps2[len(ps):]; len(got) != 5 || got[0].Text != "вопрос 1" || got[4].Kind != mailrelay.KindDelete {
		t.Fatalf("came back %+v", got)
	}
}
