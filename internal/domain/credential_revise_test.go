package domain

import (
	"errors"
	"testing"
	"time"
)

// Правка карточки меняет все поля; пустые продукт, среда и логин значат «не менять».
// Адрес собран из продукта, среды и логина: он пересчитывается, если был собран сам
// (карточка с заданным руками id его сохраняет).
func TestCredential_Revise(t *testing.T) {
	c, err := NewCredential(CredentialDTO{Product: "Проектное бюро", Env: "прод", Login: "planb", URL: "https://old.example", Note: "старая"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	got, err := c.Revise(CredentialDTO{Env: "стенд", URL: "https://new.example", Host: "192.0.2.5", Role: "админ", Rights: "полный", IssuedBy: "Анна", IssuedAt: at, Note: "новая"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Env() != EnvStage || got.Product() != "Проектное бюро" || got.Login() != "planb" {
		t.Fatalf("среда исправлена, остальное прежнее: %+v", got.DTO())
	}
	if got.ID() != "proektnoe-byuro-stage-planb" || got.ID() == c.ID() {
		t.Fatalf("id не пересчитан: %s (был %s)", got.ID(), c.ID())
	}
	d := got.DTO()
	if d.URL != "https://new.example" || d.Host != "192.0.2.5" || d.Role != "админ" || d.Rights != "полный" || d.IssuedBy != "Анна" || !d.IssuedAt.Equal(at) || d.Note != "новая" {
		t.Fatalf("правка не применилась: %+v", d)
	}
	same, err := c.Revise(CredentialDTO{Host: "192.0.2.7"})
	if err != nil || same.ID() != c.ID() || same.Env() != EnvProd {
		t.Fatalf("без смены личности id тот же: %v %+v", err, same.DTO())
	}
	if _, err := c.Revise(CredentialDTO{Env: "qa", URL: "u"}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("чужая среда: %v", err)
	}
}

func TestCredential_Revise_keepsAHandWrittenID(t *testing.T) {
	c, _ := NewCredential(CredentialDTO{ID: "crm-main", Product: "CRM", Env: "test", Login: "api", Host: "192.0.2.1"})
	got, err := c.Revise(CredentialDTO{Env: "dev", Host: "192.0.2.1"})
	if err != nil || got.ID() != "crm-main" || got.Env() != EnvDev {
		t.Fatalf("%v %+v", err, got.DTO())
	}
}
