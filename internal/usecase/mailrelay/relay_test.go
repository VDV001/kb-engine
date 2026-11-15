package mailrelay_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

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
