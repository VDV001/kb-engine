package userauth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// Телефон (#622) входит своим токеном, и тот открывает только ящик: реестр
// телефонов — отдельный объект, токен человека его не знает, и наоборот.
func TestDevices(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := id(t, "owner")
	own, err := userauth.Add(ctx, s, owner)
	if err != nil {
		t.Fatal(err)
	}
	dev, tok, err := userauth.AddDevice(ctx, s, owner)
	if err != nil || dev == "" || len(tok) < 32 {
		t.Fatalf("AddDevice = %q, %d chars, %v", dev, len(tok), err)
	}
	dev2, tok2, err := userauth.AddDevice(ctx, s, owner)
	if err != nil || dev2 == dev || tok2 == tok {
		t.Fatalf("second device = %q, %v; want another id and token", dev2, err)
	}
	devices := userauth.NewRegistry(ctx, s, userauth.WithKey(userauth.DevicesKey))
	people := userauth.NewRegistry(ctx, s)
	if got, ok := devices.Lookup(tok); !ok || got != owner {
		t.Errorf("device lookup = %v, %v", got, ok)
	}
	if _, ok := people.Lookup(tok); ok {
		t.Error("a phone token opens the whole base")
	}
	if _, ok := devices.Lookup(own); ok {
		t.Error("the person's token passes as a phone")
	}
	list, err := userauth.Devices(ctx, s, owner)
	if err != nil || len(list) != 2 || list[0].Device != dev {
		t.Fatalf("Devices = %v, %v", list, err)
	}
	if err := userauth.RevokeDevice(ctx, s, owner, dev); err != nil {
		t.Fatal(err)
	}
	if err := userauth.RevokeDevice(ctx, s, owner, dev); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Errorf("second revoke = %v", err)
	}
	other := id(t, "other")
	if err := userauth.RevokeDevice(ctx, s, other, dev2); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Errorf("revoke of someone else's phone = %v", err)
	}
	fresh := userauth.NewRegistry(ctx, s, userauth.WithKey(userauth.DevicesKey))
	if _, ok := fresh.Lookup(tok); ok {
		t.Error("revoked phone still enters")
	}
	if got, ok := fresh.Lookup(tok2); !ok || got != owner {
		t.Error("the other phone lost access")
	}
}

// Отзыв человека закрывает и его телефоны (ревью #630): иначе телефон, в том
// числе украденный, читал бы ящик дальше, а отвязать его было бы нечем.
func TestRevokePersonDropsPhones(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := id(t, "owner")
	if _, err := userauth.Add(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	_, tok, err := userauth.AddDevice(ctx, s, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := userauth.Revoke(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	if _, ok := userauth.NewRegistry(ctx, s, userauth.WithKey(userauth.DevicesKey)).Lookup(tok); ok {
		t.Error("the phone of a revoked person still enters")
	}
	if people := userauth.NewRegistry(ctx, s); people.Has(owner) {
		t.Error("revoked person is still known")
	}
}

// Не больше MaxDevices телефонов на человека: реестр общий для всех (ревью #630).
func TestDevicesLimit(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := id(t, "owner")
	for i := range userauth.MaxDevices {
		if _, _, err := userauth.AddDevice(ctx, s, owner); err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}
	if _, _, err := userauth.AddDevice(ctx, s, owner); !errors.Is(err, domain.ErrMailTooManyPhones) {
		t.Errorf("over the limit: %v", err)
	}
	list, _ := userauth.Devices(ctx, s, owner)
	if len(list) != userauth.MaxDevices || list[0].At.IsZero() {
		t.Errorf("list %d, first issued at %v; want the issue time", len(list), list[0].At)
	}
}

// Invalidate: следующий Lookup читает реестр заново, а не ждет минуту.
func TestRegistryInvalidate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := id(t, "owner")
	r := userauth.NewRegistry(ctx, s, userauth.WithKey(userauth.DevicesKey))
	r.Lookup("warm-up")
	_, tok, err := userauth.AddDevice(ctx, s, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup(tok); ok {
		t.Fatal("cache not warm: the test proves nothing")
	}
	r.Invalidate()
	if _, ok := r.Lookup(tok); !ok {
		t.Error("new phone unknown after Invalidate")
	}
}
