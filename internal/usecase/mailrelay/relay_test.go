package mailrelay_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// box is the mailbox in memory: номера подряд, сторона — у каждого сообщения.
type box struct {
	msgs    []mailrelay.Message
	postErr error
}

func (b *box) Since(_ context.Context, after int64) (mailrelay.Page, error) {
	var p mailrelay.Page
	for _, m := range b.msgs {
		if m.Seq > after {
			p.Messages = append(p.Messages, m)
		}
	}
	return p, nil
}

func (b *box) Post(_ context.Context, chat string, sealed []byte) (int64, error) {
	if b.postErr != nil {
		return 0, b.postErr
	}
	seq := int64(len(b.msgs) + 1)
	b.msgs = append(b.msgs, mailrelay.Message{Seq: seq, Chat: chat, From: domain.MailDesk, Sealed: sealed})
	return seq, nil
}

// phone puts a message of the phone: «шифр» — JSON с отправителем и счетчиком.
func (b *box) phone(chat, sender string, counter uint64, p mailrelay.Payload) {
	plain, _ := json.Marshal(p)
	sealed, _ := json.Marshal(fakeSealed{Sender: sender, Counter: counter, Plain: plain})
	b.msgs = append(b.msgs, mailrelay.Message{Seq: int64(len(b.msgs) + 1), Chat: chat, From: domain.MailPhone, Sealed: sealed})
}

type fakeSealed struct {
	Sender  string
	Counter uint64
	Plain   []byte
	Bad     bool // подделка или чужой ключ
}

// crypto opens fakeSealed; Seal пишет сторону desk и переданный счетчик.
type crypto struct{}

func (crypto) Open(_ string, _ domain.MailSide, sealed []byte) (mailrelay.Opened, error) {
	var s fakeSealed
	if err := json.Unmarshal(sealed, &s); err != nil || s.Bad {
		return mailrelay.Opened{}, errors.New("not authentic")
	}
	return mailrelay.Opened{Sender: s.Sender, Counter: s.Counter, Plain: s.Plain}, nil
}

func (crypto) Seal(_ string, counter uint64, plain []byte) ([]byte, error) {
	return json.Marshal(fakeSealed{Sender: "desk", Counter: counter, Plain: plain})
}

// asker records questions and answers «ответ на <вопрос>» в чате окна w-<chat>.
type asker struct {
	asked []string
	fail  error
}

func (a *asker) Ask(_ context.Context, chat, question string) (string, string, error) {
	a.asked = append(a.asked, chat+"|"+question)
	if a.fail != nil {
		return "", "", a.fail
	}
	if chat == "" {
		chat = fmt.Sprintf("w-%d", len(a.asked))
	}
	return chat, "ответ на " + question, nil
}

type store struct {
	st    mailrelay.State
	saves []mailrelay.State
}

func (s *store) Load() (mailrelay.State, error) { return s.st, nil }
func (s *store) Save(st mailrelay.State) error {
	s.st = st
	s.saves = append(s.saves, st)
	return nil
}

func newRelay(b *box, a *asker, s *store) mailrelay.Relay {
	return mailrelay.Relay{Box: b, Crypto: crypto{}, Asker: a, Store: s}
}

// replies decodes what the desk posted.
func replies(t *testing.T, b *box) []string {
	t.Helper()
	var out []string
	for _, m := range b.msgs {
		if m.From != domain.MailDesk {
			continue
		}
		var s fakeSealed
		var p mailrelay.Payload
		if json.Unmarshal(m.Sealed, &s) != nil || json.Unmarshal(s.Plain, &p) != nil {
			t.Fatalf("desk posted garbage: %s", m.Sealed)
		}
		out = append(out, fmt.Sprintf("%s|%d|%s|%s", m.Chat, s.Counter, p.Kind, p.Text))
	}
	return out
}

func q(text string) mailrelay.Payload {
	return mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: text}
}

// Вопрос телефона — ход модели в чате окна, ответ шифруется и уходит в тот же
// чат ящика; второй вопрос того же чата — тот же чат окна; счетчик окна растет.
func TestRelayAnswersThePhone(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	b.phone("c-1", "A", 2, q("еще"))
	b.phone("c-2", "A", 3, q("другой чат"))
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"|привет", "w-1|еще", "|другой чат"}; !slices.Equal(a.asked, want) {
		t.Errorf("asked %q, want %q", a.asked, want)
	}
	want := []string{"c-1|1|answer|ответ на привет", "c-1|2|answer|ответ на еще", "c-2|3|answer|ответ на другой чат"}
	if got := replies(t, b); !slices.Equal(got, want) {
		t.Errorf("replies %q, want %q", got, want)
	}
	if s.st.After != 6 || s.st.DeskCounter != 3 || len(s.st.Outbox) != 0 {
		t.Errorf("state %+v", s.st)
	}
	// Следующий круг: свое (desk) не читается вопросом, старое не повторяется.
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil || len(a.asked) != 3 {
		t.Errorf("second tick asked again: %q, %v", a.asked, err)
	}
}

// Повтор (тот же или меньший счетчик отправителя) и подделка — не вопрос;
// счетчики у отправителей свои.
func TestRelayDropsReplaysAndForgeries(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 5, q("раз"))
	b.phone("c-1", "A", 5, q("раз")) // сервер повторил
	b.phone("c-1", "A", 4, q("старое"))
	b.phone("c-1", "B", 1, q("от второго телефона"))
	plain, _ := json.Marshal(q("подделка"))
	bad, _ := json.Marshal(fakeSealed{Sender: "A", Counter: 9, Plain: plain, Bad: true})
	b.msgs = append(b.msgs, mailrelay.Message{Seq: 5, Chat: "c-1", From: domain.MailPhone, Sealed: bad})
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"|раз", "w-1|от второго телефона"}; !slices.Equal(a.asked, want) {
		t.Errorf("asked %q, want %q", a.asked, want)
	}
}

// Номер сообщения сохраняется до хода модели: сбой посреди хода не запустит
// модель второй раз (решение 15.11: повтор — платный запуск).
func TestRelaySavesBeforeAsking(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	asked := 0
	r := newRelay(b, &asker{}, s)
	r.Asker = askFunc(func() error {
		asked++
		if s.st.After != 1 {
			t.Errorf("asked before saving the cursor: after = %d", s.st.After)
		}
		return nil
	})
	if err := r.Tick(t.Context()); err != nil || asked != 1 {
		t.Fatalf("asked %d times, %v", asked, err)
	}
}

type askFunc func() error

func (f askFunc) Ask(context.Context, string, string) (string, string, error) {
	return "w", "ok", f()
}

// Модель отказала — телефон получает ошибку словами, а не тишину.
func TestRelayReportsAskFailure(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	a := &asker{fail: errors.New("модель не отвечает")}
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, b); len(got) != 1 || got[0] != "c-1|1|error|модель не отвечает" {
		t.Errorf("replies %q", got)
	}
}

// Ответ не ушел (сеть) — остается в исходящих и уходит следующим кругом тем же
// счетчиком; модель второй раз не спрашивается.
func TestRelayOutboxSurvivesPostFailure(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	b.postErr = errors.New("нет сети")
	if err := newRelay(b, a, s).Tick(t.Context()); err == nil {
		t.Fatal("post failure not reported")
	}
	if len(s.st.Outbox) != 1 {
		t.Fatalf("outbox %+v", s.st.Outbox)
	}
	b.postErr = nil
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, b); len(got) != 1 || got[0] != "c-1|1|answer|ответ на привет" || len(a.asked) != 1 || len(s.st.Outbox) != 0 {
		t.Errorf("replies %q, asked %q, outbox %+v", got, a.asked, s.st.Outbox)
	}
}

// Чужие виды сообщений и нечитаемый текст — пропуск без хода модели.
func TestRelayIgnoresUnknownPayloads(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 1, mailrelay.Payload{Kind: "typing"})
	b.phone("c-1", "A", 2, mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "  "})
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(a.asked) != 0 || s.st.After != 2 {
		t.Errorf("asked %q, after %d", a.asked, s.st.After)
	}
}

// --- ревью #635

// sealingCrypto seals with a fresh «нонс» every time, как настоящий AES-GCM.
type sealingCrypto struct {
	crypto
	n int
}

func (c *sealingCrypto) Seal(chat string, counter uint64, plain []byte) ([]byte, error) {
	c.n++
	return json.Marshal(fakeSealed{Sender: fmt.Sprintf("desk-%d", c.n), Counter: counter, Plain: plain})
}

// attemptBox records every post attempt; первые fails попыток падают.
type attemptBox struct {
	box
	attempts [][]byte
	fails    []error
}

func (b *attemptBox) Post(ctx context.Context, chat string, sealed []byte) (int64, error) {
	b.attempts = append(b.attempts, sealed)
	if len(b.fails) > 0 {
		err := b.fails[0]
		b.fails = b.fails[1:]
		return 0, err
	}
	return b.box.Post(ctx, chat, sealed)
}

// Повтор отправки — те же байты: сервер склеивает одинаковую строку, и ответ,
// дошедший до сервера при потерянном ответе сервера, не задвоится.
func TestRelayRetriesWithTheSameBytes(t *testing.T) {
	b, a, s := &attemptBox{fails: []error{errors.New("таймаут")}}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	r := mailrelay.Relay{Box: b, Crypto: &sealingCrypto{}, Asker: a, Store: s}
	if err := r.Tick(t.Context()); err == nil {
		t.Fatal("post failure not reported")
	}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(b.attempts) != 2 || string(b.attempts[0]) != string(b.attempts[1]) {
		t.Errorf("retry sealed anew: %d attempts, same bytes %t", len(b.attempts), len(b.attempts) == 2 && string(b.attempts[0]) == string(b.attempts[1]))
	}
}

// Сервер отказал насовсем (ErrRefused: велико, неверный чат) — ответ уходит
// из исходящих, ящик не затыкается: следующий вопрос получает ответ.
func TestRelayDropsRefusedReplies(t *testing.T) {
	b, a, s := &attemptBox{fails: []error{fmt.Errorf("%w: 413", mailrelay.ErrRefused)}}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("раз"))
	r := newRelay(&b.box, a, s)
	r.Box = b
	_ = r.Tick(t.Context())
	if len(s.st.Outbox) != 0 {
		t.Fatalf("refused reply stays in the outbox: %+v", s.st.Outbox)
	}
	b.phone("c-1", "A", 2, q("два"))
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, &b.box); len(got) != 1 || got[0] != "c-1|2|answer|ответ на два" {
		t.Errorf("after a refusal: %q", got)
	}
}

// Длинный ответ обрезается под предел ящика с пометкой, а не отбивается сервером.
func TestRelayTrimsLongAnswers(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("длинно"))
	r := newRelay(b, &asker{}, s)
	r.Asker = longAsker(strings.Repeat("я", 500))
	r.MaxText = 100
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := replies(t, b)
	if len(got) != 1 {
		t.Fatalf("replies %q", got)
	}
	text := strings.SplitN(got[0], "|", 4)[3]
	if len(text) > 100 || !strings.Contains(text, "окне") {
		t.Errorf("trimmed to %d bytes: %q", len(text), text)
	}
}

type longAsker string

func (l longAsker) Ask(context.Context, string, string) (string, string, error) {
	return "w", string(l), nil
}

// Новое состояние (первая привязка, новый ключ, другой сервер под тем же id)
// не отвечает на вопросы до своего начала: иначе — платный ход на каждый
// старый вопрос журнала. Номер ящика при новом ключе начинается заново.
func TestRelayFreshStateSkipsOldQuestions(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	b, a, s := &box{}, &asker{}, &store{st: mailrelay.State{After: 500, KeyID: "old", DeskCounter: 7}}
	b.phone("c-1", "A", 1, q("до привязки"))
	b.msgs[0].At = now.Add(-time.Hour)
	b.phone("c-1", "A", 2, q("после"))
	b.msgs[1].At = now.Add(time.Minute)
	r := newRelay(b, a, s)
	r.KeyID, r.Now = "new", func() time.Time { return now }
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"|после"}; !slices.Equal(a.asked, want) {
		t.Errorf("asked %q, want %q", a.asked, want)
	}
	if s.st.KeyID != "new" || s.st.DeskCounter != 8 {
		t.Errorf("state %+v: the desk counter must keep growing across keys", s.st)
	}
}

// Страница без вопросов — одна запись состояния, а не по записи на сообщение.
func TestRelaySavesOncePerQuietPage(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	for i := range 20 {
		b.phone("c-1", "A", uint64(i+1), mailrelay.Payload{Kind: "typing"})
	}
	if err := newRelay(b, a, s).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(s.saves); n > 1 || s.st.After != 20 {
		t.Errorf("%d saves for a page without questions, after %d", n, s.st.After)
	}
}

// memory records decisions.
type memory struct{ got []string }

func (m *memory) Decide(_ context.Context, id string, yes bool, text string) error {
	m.got = append(m.got, fmt.Sprintf("%s|%t|%s", id, yes, text))
	return nil
}

// «Запомнить о Вас?» из ответа телефону уходит в его чат ящика; заметка о чате
// окна, которого телефон не знает, пропадает (#647).
func TestRelayOffersFactsToThePhone(t *testing.T) {
	b, s := &box{}, &store{st: mailrelay.State{Chats: map[string]string{"c-1": "w-1"}}}
	n := []mailrelay.Note{
		{Chat: "w-1", Payload: mailrelay.Payload{Kind: mailrelay.KindRemember, Facts: []mailrelay.Fact{{ID: "f1", Title: "Работа", Text: "Ведет закупки"}}}},
		{Chat: "w-9", Payload: mailrelay.Payload{Kind: mailrelay.KindRemember, Facts: []mailrelay.Fact{{ID: "f9", Text: "чужое"}}}},
	}
	r := newRelay(b, &asker{}, s)
	r.Notes = n
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, b); len(got) != 1 || got[0] != "c-1|1|remember|" {
		t.Fatalf("mailbox %v", got)
	}
	if o := s.st.Offered["c-1"]; len(o) != 1 || o[0] != "f1" {
		t.Errorf("offered %v", s.st.Offered)
	}
}

// Решение телефона — только по предложенному ему факту и один раз: телефон не
// удалит чужой или уже одобренный факт по id (#647).
func TestRelayDecidesOnlyOfferedFacts(t *testing.T) {
	b := &box{}
	s := &store{st: mailrelay.State{Chats: map[string]string{"c-1": "w-1"}, Offered: map[string][]string{"c-1": {"f1"}}}}
	mem := &memory{}
	r := newRelay(b, &asker{}, s)
	r.Memory = mem
	decide := func(counter uint64, id, yes, text string) {
		b.phone("c-1", "A", counter, mailrelay.Payload{Kind: mailrelay.KindMemory, Text: yes, Facts: []mailrelay.Fact{{ID: id, Text: text}}})
	}
	decide(1, "f1", mailrelay.MemoryYes, "Ведет закупки и тендеры")
	decide(2, "f2", mailrelay.MemoryNo, "") // не предлагался
	decide(3, "f1", mailrelay.MemoryNo, "") // уже решен
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(mem.got) != 1 || mem.got[0] != "f1|true|Ведет закупки и тендеры" {
		t.Fatalf("decided %v", mem.got)
	}
	got := payloads(t, b)
	if len(got) != 3 || got[0].Kind != mailrelay.KindMemory || got[0].Text != mailrelay.MemorySaved || got[0].Facts[0].ID != "f1" ||
		got[1].Text != mailrelay.MemoryGone || got[1].Facts[0].ID != "f2" || got[2].Text != mailrelay.MemoryGone {
		t.Fatalf("replies %+v", got)
	}
	if len(s.st.Offered["c-1"]) != 0 {
		t.Errorf("decided fact still offered: %v", s.st.Offered)
	}
}

// gone answers that the fact is already decided in the window.
type gone struct{}

func (gone) Decide(context.Context, string, bool, string) error { return domain.ErrFactDecided }

// Факт решили в окне — решение телефона не проходит, ему уходит gone (карточку
// снять), из предложенного факт убирается; неразобранное — bad с id; окно без
// памяти — unsupported (ревью #655).
func TestRelayDecisionCodes(t *testing.T) {
	b := &box{}
	s := &store{st: mailrelay.State{Chats: map[string]string{"c-1": "w-1"}, Offered: map[string][]string{"c-1": {"f1", "f2"}}}}
	r := newRelay(b, &asker{}, s)
	r.Memory = gone{}
	b.phone("c-1", "A", 1, mailrelay.Payload{Kind: mailrelay.KindMemory, Text: mailrelay.MemoryNo, Facts: []mailrelay.Fact{{ID: "f1"}}})
	b.phone("c-1", "A", 2, mailrelay.Payload{Kind: mailrelay.KindMemory, Text: "maybe", Facts: []mailrelay.Fact{{ID: "f2"}}})
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if len(got) != 2 || got[0].Text != mailrelay.MemoryGone || got[1].Text != mailrelay.MemoryBad || len(got[1].Facts) != 1 || got[1].Facts[0].ID != "f2" {
		t.Fatalf("replies %+v", got)
	}
	if o := s.st.Offered["c-1"]; len(o) != 1 || o[0] != "f2" {
		t.Errorf("decided in the window — still offered: %v", o)
	}
	r.Memory = nil
	b.phone("c-1", "A", 3, mailrelay.Payload{Kind: mailrelay.KindMemory, Text: mailrelay.MemoryYes, Facts: []mailrelay.Fact{{ID: "f2"}}})
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := payloads(t, b); got[len(got)-1].Text != mailrelay.MemoryUnsupported {
		t.Fatalf("no Memory: %+v", got[len(got)-1])
	}
}

// tight is crypto with the mailbox limit: больше 1000 байт — ErrMailTooLarge.
type tight struct{ crypto }

func (t tight) Seal(chat string, counter uint64, plain []byte) ([]byte, error) {
	if len(plain) > 1000 {
		return nil, domain.ErrMailTooLarge
	}
	return t.crypto.Seal(chat, counter, plain)
}

// Предложение, что не лезет в ящик, пропускается, а не срывает круг: вопросы
// телефона в этом круге все равно отвечаются (ревью #655).
func TestRelayOversizedOfferDoesNotStopTheTick(t *testing.T) {
	b, s := &box{}, &store{st: mailrelay.State{Chats: map[string]string{"c-1": "w-1"}}}
	b.phone("c-2", "A", 1, q("сроки?"))
	r := newRelay(b, &asker{}, s)
	r.Crypto = tight{}
	r.Notes = []mailrelay.Note{{Chat: "w-1", Payload: mailrelay.Payload{Kind: mailrelay.KindRemember, Facts: []mailrelay.Fact{{ID: "f1", Text: strings.Repeat("я", 2000)}}}}}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, b); len(got) != 1 || !strings.Contains(got[0], "answer|ответ на сроки?") {
		t.Fatalf("mailbox %v", got)
	}
}
