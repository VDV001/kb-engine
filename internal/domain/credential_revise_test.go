package domain

import (
	"errors"
	"testing"
	"time"
)

// Правка карточки: продукт, среда и логин не меняются (из них собран id), остальное да.
func TestCredential_Revise(t *testing.T) {
	c, err := NewCredential(CredentialDTO{Product: "Проектное бюро", Env: "стенд", Login: "planb", URL: "https://old.example", Note: "старая"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	got, err := c.Revise(CredentialDTO{
		Product: "Другой", Env: EnvProd, Login: "other", ID: "hijack",
		URL: "https://new.example", Host: "192.0.2.5", Role: "админ", Rights: "полный", IssuedBy: "Анна", IssuedAt: at, Note: "новая",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != c.ID() || got.Product() != "Проектное бюро" || got.Env() != EnvStage || got.Login() != "planb" {
		t.Fatalf("личность карточки изменилась: %+v", got.DTO())
	}
	d := got.DTO()
	if d.URL != "https://new.example" || d.Host != "192.0.2.5" || d.Role != "админ" || d.Rights != "полный" || d.IssuedBy != "Анна" || !d.IssuedAt.Equal(at) || d.Note != "новая" {
		t.Fatalf("правка не применилась: %+v", d)
	}
	if _, err := c.Revise(CredentialDTO{}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("без адреса и хоста карточка невалидна: %v", err)
	}
}
