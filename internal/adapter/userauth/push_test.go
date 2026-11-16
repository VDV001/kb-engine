package userauth_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// Подписка на push живет в записи телефона: отвязка телефона уносит и ее (#622).
func TestPush(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner, other := id(t, "owner"), id(t, "other")
	for _, u := range []domain.UserID{owner, other} {
		if _, err := userauth.Add(ctx, s, u); err != nil {
			t.Fatal(err)
		}
	}
	dev, tok, err := userauth.AddDevice(ctx, s, owner)
	if err != nil {
		t.Fatal(err)
	}
	dev2, _, _ := userauth.AddDevice(ctx, s, owner)
	foreign, _, _ := userauth.AddDevice(ctx, s, other)
	key := append([]byte{4}, bytes.Repeat([]byte{7}, 64)...)
	sub, err := domain.NewPushSubscription("https://fcm.googleapis.com/fcm/send/a", key, bytes.Repeat([]byte{9}, 16))
	if err != nil {
		t.Fatal(err)
	}

	devices := userauth.NewRegistry(ctx, s, userauth.WithKey(userauth.DevicesKey))
	if got, d, ok := devices.LookupDevice(tok); !ok || got != owner || d != dev {
		t.Fatalf("LookupDevice = %v, %q, %v; want %v, %q", got, d, ok, owner, dev)
	}

	if err := userauth.SetPush(ctx, s, owner, foreign, sub); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("подписка на чужой телефон: %v", err)
	}
	if err := userauth.SetPush(ctx, s, owner, "p-none", sub); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("подписка на несуществующий телефон: %v", err)
	}
	if err := userauth.SetPush(ctx, s, owner, dev, sub); err != nil {
		t.Fatal(err)
	}
	got, err := userauth.Pushes(ctx, s, owner)
	if err != nil || len(got) != 1 || got[0].Device != dev || got[0].Sub.Endpoint() != sub.Endpoint() ||
		!bytes.Equal(got[0].Sub.P256dh(), key) || !bytes.Equal(got[0].Sub.Auth(), sub.Auth()) {
		t.Fatalf("Pushes = %+v, %v", got, err)
	}
	if p, _ := userauth.Pushes(ctx, s, other); len(p) != 0 {
		t.Fatalf("чужие подписки: %+v", p)
	}
	// Без подписки телефон в списке не значится; повторная отписка не ошибка.
	if err := userauth.ClearPush(ctx, s, owner, dev2); err != nil {
		t.Fatalf("отписка без подписки: %v", err)
	}
	if err := userauth.ClearPush(ctx, s, owner, dev); err != nil {
		t.Fatal(err)
	}
	if p, _ := userauth.Pushes(ctx, s, owner); len(p) != 0 {
		t.Fatalf("после отписки: %+v", p)
	}
	if err := userauth.ClearPush(ctx, s, owner, foreign); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("отписка чужого телефона: %v", err)
	}

	if err := userauth.SetPush(ctx, s, owner, dev, sub); err != nil {
		t.Fatal(err)
	}
	if err := userauth.RevokeDevice(ctx, s, owner, dev); err != nil {
		t.Fatal(err)
	}
	if p, _ := userauth.Pushes(ctx, s, owner); len(p) != 0 {
		t.Fatalf("отвязанный телефон остался в рассылке: %+v", p)
	}
}

// Стирание подписки при отвязке может не дойти (сбой хранилища между двумя
// записями) — отвязанный телефон все равно не получает уведомлений.
func TestPushes_skipsRevokedPhoneEvenIfClearFailed(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := id(t, "owner")
	if _, err := userauth.Add(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	dev, _, _ := userauth.AddDevice(ctx, s, owner)
	sub, _ := domain.NewPushSubscription("https://fcm.googleapis.com/fcm/send/a", append([]byte{4}, bytes.Repeat([]byte{7}, 64)...), bytes.Repeat([]byte{9}, 16))
	if err := userauth.SetPush(ctx, s, owner, dev, sub); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, userauth.PushKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := userauth.RevokeDevice(ctx, s, owner, dev); err != nil {
		t.Fatal(err)
	}
	// Вернуть подписку, как если бы стирание не дошло.
	now, _ := s.Get(ctx, userauth.PushKey)
	if _, err := s.Put(ctx, userauth.PushKey, before.Data, now.ETag); err != nil {
		t.Fatal(err)
	}
	if p, err := userauth.Pushes(ctx, s, owner); err != nil || len(p) != 0 {
		t.Fatalf("Pushes = %+v, %v", p, err)
	}
}
