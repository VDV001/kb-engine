package mailrelay_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// notifier records calls and how many desk messages were in the box then.
type notifier struct {
	b     *box
	calls []int
	err   error
}

func (n *notifier) Notify(context.Context) error {
	n.calls = append(n.calls, len(n.b.msgs))
	return n.err
}

// Итог хода модели зовет телефон push — после того как ответ уже в ящике:
// телефон, открыв уведомление, должен его застать (#622).
func TestRelayNotifiesAfterTheAnswer(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	n := &notifier{b: b}
	r := newRelay(b, a, s)
	r.Notifier = n
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 1 || n.calls[0] != 2 {
		t.Fatalf("notify calls %v; want one, after the answer (2 messages in the box)", n.calls)
	}
}

// Поиск — не ход модели: телефон ждет его с открытой страницей.
func TestRelayDoesNotNotifyForSearch(t *testing.T) {
	b, a := &box{}, &asker{}
	b.phone("s1", "p", 1, mailrelay.Payload{Kind: mailrelay.KindSearch, Text: "договор"})
	n := &notifier{b: b}
	r := newRelay(b, a, &store{})
	r.Finder, r.Notifier = &finder{}, n
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 0 {
		t.Fatalf("notify for a search: %v", n.calls)
	}
}

// Сбой хода модели — тоже итог: человек ждет, и ему стоит узнать.
func TestRelayNotifiesAfterAskFailure(t *testing.T) {
	b, s := &box{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	n := &notifier{b: b}
	r := mailrelay.Relay{Box: b, Crypto: crypto{}, Asker: askFunc(func() error { return errors.New("модель недоступна") }), Store: s, Notifier: n}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("notify calls %v", n.calls)
	}
}

// Ответ не ушел — push нет; ушел следующим кругом — push тогда.
func TestRelayNotifiesWhenTheAnswerFinallyGoes(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	b.postErr = errors.New("нет сети")
	n := &notifier{b: b}
	r := newRelay(b, a, s)
	r.Notifier = n
	_ = r.Tick(t.Context())
	if len(n.calls) != 0 {
		t.Fatalf("notify before the answer went: %v", n.calls)
	}
	b.postErr = nil
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("notify calls %v", n.calls)
	}
}

// Сбой push — в журнал; ящик и следующий круг он не держит.
func TestRelayNotifyFailureIsOnlyLogged(t *testing.T) {
	b, a, s := &box{}, &asker{}, &store{}
	b.phone("c-1", "A", 1, q("привет"))
	n := &notifier{b: b, err: errors.New("сервер push недоступен")}
	var log strings.Builder
	r := newRelay(b, a, s)
	r.Notifier, r.Logf = n, func(f string, args ...any) { fmt.Fprintf(&log, f+"\n", args...) }
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("push failure stopped the relay: %v", err)
	}
	if len(s.st.Outbox) != 0 || !strings.Contains(log.String(), "сервер push недоступен") {
		t.Fatalf("outbox %+v, log %q", s.st.Outbox, log.String())
	}
	if err := r.Tick(t.Context()); err != nil || len(n.calls) != 1 {
		t.Fatalf("second tick: %v, calls %v (push не повторяется)", err, n.calls)
	}
}
