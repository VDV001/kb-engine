package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// «Поменял прод на стенд, сохранить — credential already exists: proektnoe-…»:
// занятый адрес человеку называют словами окна, без внутреннего id и английского.
func TestAccessDesk_takenAddressIsSaidInRussian(t *testing.T) {
	d, _ := newAccess(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	prod := accessIn{Product: "Проектное бюро", Env: "prod", Login: "planb", Host: "198.51.100.7"}
	stage := accessIn{Product: "Проектное бюро", Env: "stage", Login: "planb", Host: "203.0.113.9"}
	for _, in := range []accessIn{prod, stage} {
		if err := d.Add(in, secretPW); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"правка: среда совпала с другой карточкой", func() error {
			return d.Update("proektnoe-byuro-prod-planb", accessIn{Product: "Проектное бюро", Env: "stage", Login: "planb", Host: "198.51.100.7"}, "")
		}},
		{"добавление: такой доступ уже есть", func() error { return d.Add(prod, secretPW) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if !errors.Is(err, domain.ErrCredentialExists) {
				t.Fatalf("errors.Is(ErrCredentialExists) потерян: %v", err)
			}
			msg := err.Error()
			if strings.Contains(msg, "already exists") || strings.Contains(msg, "proektnoe-byuro") {
				t.Errorf("английский текст или внутренний id в сообщении: %q", msg)
			}
			if !strings.Contains(msg, "уже есть") || !strings.Contains(msg, "продукт, среду или логин") {
				t.Errorf("не сказано, что делать: %q", msg)
			}
		})
	}
}

// Пароль под этим адресом остался в Связке без карточки: это другой отказ.
func TestAccessDesk_strayKeychainPasswordIsSaidInRussian(t *testing.T) {
	d, _ := newAccess(t)
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	d.svc.Secrets.(memSecrets)["crm-test-api"] = "left-behind"
	err := d.Add(accessIn{Product: "CRM", Env: "test", Login: "api", Host: "203.0.113.5"}, secretPW)
	if !errors.Is(err, domain.ErrCredentialExists) || !strings.Contains(err.Error(), "в Связке ключей") || strings.Contains(err.Error(), "crm-test-api") {
		t.Fatalf("сообщение: %v", err)
	}
}
