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
	if err != nil || len(list) != 2 {
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
