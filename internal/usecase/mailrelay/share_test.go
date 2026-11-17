package mailrelay_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

// histories is the window's chats as HistoryOf reads them; turn records the
// chat's history and gives the queued turn — только последний обмен (ревью #675).
type histories map[string][]mailrelay.Exchange

func (h histories) of(chat string) ([]mailrelay.Exchange, error) {
	if x, ok := h[chat]; ok {
		return x, nil
	}
	return nil, fmt.Errorf("no chat %s", chat)
}

func (h histories) turn(chat, title string, isNew bool, x []mailrelay.Exchange) mailrelay.WindowTurn {
	h[chat] = x
	return mailrelay.WindowTurn{Chat: chat, Title: title, New: isNew, Last: x[len(x)-1]}
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
	hist := histories{}
	r.HistoryOf = hist.of
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-7", "Поставки", true, exchanges(2))}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	chats, ps := deskPosts(t, b)
	// История — одним сообщением history, а не по два на ход: предел отправки
	// сервера — раз в секунду (ревью #675).
	if len(ps) != 1 || ps[0].Kind != mailrelay.KindHistory || ps[0].Title != "Поставки" || len(ps[0].History) != 2 ||
		ps[0].History[0].Question != "вопрос 1" || ps[0].History[1].Answer != "ответ 2" {
		t.Fatalf("posted %+v", ps)
	}
	mc := chats[0]
	if err := domain.ValidateMailChat(mc); err != nil || s.st.Chats[mc] != "w-7" {
		t.Fatalf("chat %q (%v), mapping %v", mc, err, s.st.Chats)
	}
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-7", "Поставки", true, exchanges(3))}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	chats, ps = deskPosts(t, b)
	if len(ps) != 3 || ps[1].Kind != mailrelay.KindAsked || ps[1].Text != "вопрос 3" || ps[2].Text != "ответ 3" || chats[2] != mc {
		t.Fatalf("second turn %+v", ps[1:])
	}
}

// Ход окна в чате телефона виден на телефоне; старый чат окна (до выкатки) —
// не уходит.
func TestRelaySharesWindowTurnsOfAPhoneChat(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	r := newRelay(b, &asker{}, s)
	hist := histories{}
	r.HistoryOf = hist.of
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.Turns = []mailrelay.WindowTurn{
		hist.turn("w-1", "привет", false, exchanges(2)),
		hist.turn("w-old", "старый", false, exchanges(1)),
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
	hist := histories{}
	r.HistoryOf = hist.of
	for i := range mailrelay.MaxSharedChats + 1 {
		r.Turns = []mailrelay.WindowTurn{hist.turn(fmt.Sprintf("w-%d", i), "т", true, exchanges(1))}
		if err := r.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.st.Shared) != mailrelay.MaxSharedChats {
		t.Fatalf("shared %d", len(s.st.Shared))
	}
	chats, ps := deskPosts(t, b)
	last := ps[len(ps)-1]
	if last.Kind != mailrelay.KindDelete || last.Text != mailrelay.DeleteEvicted || chats[len(chats)-1] != chats[0] {
		t.Fatalf("the oldest is not deleted on the phone: %+v in %s", last, chats[len(chats)-1])
	}
	for mc, wc := range s.st.Chats {
		if wc == "w-0" {
			t.Errorf("w-0 still linked as %s", mc)
		}
	}
	// Ход в чате, выпавшем с телефона, возвращает его целиком и освежает место.
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-0", "т", true, exchanges(2))}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ps2 := deskPosts(t, b)
	if got := ps2[len(ps):]; len(got) != 2 || got[0].Kind != mailrelay.KindHistory || len(got[0].History) != 2 || got[1].Kind != mailrelay.KindDelete {
		t.Fatalf("came back %+v", got)
	}
}

// Длинная история — пачками в предел сообщения, по порядку; длинный ответ
// укорочен, а не роняет пачку.
func TestRelayPacksLongHistory(t *testing.T) {
	b, s := &box{}, &store{}
	r := newRelay(b, &asker{}, s)
	hist := histories{}
	r.HistoryOf = hist.of
	r.MaxText = 400
	h := exchanges(10)
	h[3].Answer = strings.Repeat("я", 2000)
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-1", "т", true, h)}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ps := deskPosts(t, b)
	var got []mailrelay.Exchange
	for _, p := range ps {
		if p.Kind != mailrelay.KindHistory {
			t.Fatalf("not history: %+v", p)
		}
		if raw, _ := json.Marshal(p.History); len(raw) > r.MaxText {
			t.Errorf("pack of %d bytes over %d", len(raw), r.MaxText)
		}
		got = append(got, p.History...)
	}
	if len(ps) < 2 || len(ps) > 6 || len(got) != 10 || got[0].Question != "вопрос 1" || got[9].Question != "вопрос 10" || len(got[3].Answer) >= 2000 {
		t.Fatalf("%d packs, %d exchanges", len(ps), len(got))
	}
}

// Удаление в том же круге — ход не заводит чат заново; переименование того
// же круга — название пачки (ревью #675).
func TestRelayShareHonorsSameRoundEdits(t *testing.T) {
	b, s := &box{}, &store{}
	r := newRelay(b, &asker{}, s)
	hist := histories{}
	r.HistoryOf = hist.of
	r.Notes = []mailrelay.Note{{Chat: "w-del", Payload: mailrelay.Payload{Kind: mailrelay.KindDelete, Text: "chat"}},
		{Chat: "w-ren", Payload: mailrelay.Payload{Kind: mailrelay.KindRename, Text: "Новое"}}}
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-del", "т", true, exchanges(1)),
		hist.turn("w-ren", "старое", true, exchanges(1))}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ps := deskPosts(t, b)
	if len(ps) != 1 || ps[0].Title != "Новое" {
		t.Fatalf("posted %+v", ps)
	}
	for _, wc := range s.st.Chats {
		if wc == "w-del" {
			t.Error("deleted chat linked again")
		}
	}
}

// Вопрос с телефона освежает место чата окна; выпавший из 50 чат не помечен
// удаленным — неотправленный вопрос телефона получит ответ (ревью #675).
func TestRelayPhoneKeepsSharedChatFresh(t *testing.T) {
	b, s := &box{}, &store{}
	a := &asker{}
	r := newRelay(b, a, s)
	hist := histories{}
	r.HistoryOf = hist.of
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-first", "т", true, exchanges(1))}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := s.st.Shared[0]
	for i := range mailrelay.MaxSharedChats - 1 {
		r.Turns = []mailrelay.WindowTurn{hist.turn(fmt.Sprintf("w-%d", i), "т", true, exchanges(1))}
		if err := r.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	b.phone(first, "A", 1, q("еще вопрос"))
	r.Turns = nil
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.st.Shared[len(s.st.Shared)-1] != first {
		t.Fatalf("phone question did not refresh %s: %v", first, s.st.Shared[len(s.st.Shared)-5:])
	}
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-new", "т", true, exchanges(1))}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.st.Shared, first) {
		t.Fatal("the fresh chat was evicted")
	}
	if len(s.st.Deleted) != 0 {
		t.Errorf("evicted chat marked deleted %v — вопрос телефона в нем пропадет", s.st.Deleted)
	}
}

// Ящик говорит, что ходы легли в исходящие: работник не повторит их и не
// потеряет, если круг сорвался раньше (ревью #675). История без HistoryOf —
// последний обмен, чат все равно заводится.
func TestRelayReportsSharedTurns(t *testing.T) {
	b, s := &box{}, &store{}
	r := newRelay(b, &asker{}, s)
	hist := histories{}
	r.HistoryOf = hist.of
	shared := 0
	r.Shared = func() { shared++ }
	r.Turns = []mailrelay.WindowTurn{hist.turn("w-1", "т", true, exchanges(2))}
	if err := r.Tick(t.Context()); err != nil || shared != 1 {
		t.Fatalf("shared %d, %v", shared, err)
	}
	r.Turns = []mailrelay.WindowTurn{{Chat: "w-gone", Title: "т", New: true, Last: mailrelay.Exchange{Question: "q", Answer: "a"}}}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ps := deskPosts(t, b)
	if last := ps[len(ps)-1]; last.Kind != mailrelay.KindHistory || len(last.History) != 1 || last.History[0].Question != "q" {
		t.Errorf("history unreadable: %+v", last)
	}
	broken := newRelay(b, &asker{}, &store{})
	broken.Store = failingStore{}
	broken.Shared = func() { shared++ }
	broken.Turns = r.Turns
	if err := broken.Tick(t.Context()); err == nil || shared != 2 {
		t.Errorf("state not loaded: shared %d, %v — ходы не легли, отметки быть не должно", shared, err)
	}
}

type failingStore struct{}

func (failingStore) Load() (mailrelay.State, error) { return mailrelay.State{}, fmt.Errorf("disk") }
func (failingStore) Save(mailrelay.State) error     { return fmt.Errorf("disk") }
