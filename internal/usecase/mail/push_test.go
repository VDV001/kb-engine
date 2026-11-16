package mail_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

type fakeSubs struct {
	mu   sync.Mutex
	subs map[string]domain.PushSubscription // device → подписка, один человек
}

func (f *fakeSubs) SetPush(_ context.Context, _ domain.UserID, device string, sub domain.PushSubscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[device] = sub
	return nil
}

func (f *fakeSubs) ClearPush(_ context.Context, _ domain.UserID, device string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, device)
	return nil
}

func (f *fakeSubs) Pushes(context.Context, domain.UserID) ([]mail.PhonePush, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []mail.PhonePush
	for d, s := range f.subs {
		out = append(out, mail.PhonePush{Device: d, Sub: s})
	}
	return out, nil
}

type fakePusher struct {
	mu       sync.Mutex
	sent     []string // адреса
	payloads [][]byte
	fail     map[string]error
}

func (f *fakePusher) Send(_ context.Context, sub domain.PushSubscription, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sub.Endpoint())
	f.payloads = append(f.payloads, payload)
	return f.fail[sub.Endpoint()]
}

var (
	pushKey  = append([]byte{4}, bytes.Repeat([]byte{1}, 64)...)
	pushAuth = bytes.Repeat([]byte{2}, 16)
)

func sub(t *testing.T, endpoint string) domain.PushSubscription {
	t.Helper()
	s, err := domain.NewPushSubscription(endpoint, pushKey, pushAuth)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var who = func() domain.UserID {
	id, err := domain.NewUserID("daniil")
	if err != nil {
		panic(err)
	}
	return id
}()

func TestSubscribePush(t *testing.T) {
	subs := &fakeSubs{subs: map[string]domain.PushSubscription{}}
	svc := mail.Service{Subs: subs}
	ctx := context.Background()
	if err := svc.SubscribePush(ctx, who, domain.MailDesk, "p-1", "https://fcm.googleapis.com/fcm/send/a", pushKey, pushAuth); !errors.Is(err, domain.ErrMailPhoneOnly) {
		t.Fatalf("компьютер подписал телефон: %v", err)
	}
	if err := svc.SubscribePush(ctx, who, domain.MailPhone, "p-1", "https://evil.example/a", pushKey, pushAuth); !errors.Is(err, domain.ErrBadPushSubscription) {
		t.Fatalf("чужой адрес: %v", err)
	}
	if err := svc.SubscribePush(ctx, who, domain.MailPhone, "p-1", "https://fcm.googleapis.com/fcm/send/a", pushKey, pushAuth); err != nil {
		t.Fatal(err)
	}
	if s, ok := subs.subs["p-1"]; !ok || s.Endpoint() != "https://fcm.googleapis.com/fcm/send/a" {
		t.Fatalf("подписка %v", subs.subs)
	}
	if err := svc.UnsubscribePush(ctx, who, domain.MailDesk, "p-1"); !errors.Is(err, domain.ErrMailPhoneOnly) {
		t.Fatalf("компьютер отписал телефон: %v", err)
	}
	if err := svc.UnsubscribePush(ctx, who, domain.MailPhone, "p-1"); err != nil || len(subs.subs) != 0 {
		t.Fatalf("отписка: %v, %v", err, subs.subs)
	}
}

func TestNotify(t *testing.T) {
	const a, b, c = "https://fcm.googleapis.com/fcm/send/a", "https://web.push.apple.com/b", "https://updates.push.services.mozilla.com/wpush/v2/c"
	subs := &fakeSubs{subs: map[string]domain.PushSubscription{"p-a": sub(t, a), "p-b": sub(t, b), "p-c": sub(t, c)}}
	push := &fakePusher{fail: map[string]error{
		b: domain.ErrPushGone,
		c: errors.New("timeout"),
	}}
	svc := mail.Service{Subs: subs, Push: push, Logf: func(string, ...any) {}}
	if _, err := svc.Notify(context.Background(), who, domain.MailPhone); !errors.Is(err, domain.ErrMailDeskOnly) {
		t.Fatalf("телефон разослал уведомления: %v", err)
	}
	n, err := svc.Notify(context.Background(), who, domain.MailDesk)
	if err != nil || n != 1 {
		t.Fatalf("Notify = %d, %v; доставлено должно быть 1 из 3", n, err)
	}
	slices.Sort(push.sent)
	want := []string{a, b, c}
	slices.Sort(want)
	if !slices.Equal(push.sent, want) {
		t.Fatalf("разослано %v", push.sent)
	}
	for _, p := range push.payloads {
		if string(p) != mail.NotifyAnswer {
			t.Fatalf("в уведомлении %q, а должен быть только вид события", p)
		}
	}
	if _, ok := subs.subs["p-b"]; ok {
		t.Error("забытая сервисом подписка осталась")
	}
	if _, ok := subs.subs["p-c"]; !ok {
		t.Error("подписку удалили за временную ошибку")
	}
}

func TestNotify_NoPhones(t *testing.T) {
	svc := mail.Service{Subs: &fakeSubs{subs: map[string]domain.PushSubscription{}}, Push: &fakePusher{}}
	if n, err := svc.Notify(context.Background(), who, domain.MailDesk); n != 0 || err != nil {
		t.Fatalf("Notify = %d, %v", n, err)
	}
}
