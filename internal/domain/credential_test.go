package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

var issued = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

// Среда пишется как говорят: «прод», «production», «стенд», «предпрод» — тип
// один; незнакомое слово — отказ, а не «прочее», иначе прод под опечаткой
// потерял бы красную метку.
func TestParseEnv(t *testing.T) {
	for _, c := range []struct {
		in   string
		want domain.Env
	}{
		{"prod", domain.EnvProd}, {"Прод", domain.EnvProd}, {"production", domain.EnvProd},
		{"stage", domain.EnvStage}, {"стенд", domain.EnvStage}, {"staging", domain.EnvStage}, {"предпрод", domain.EnvStage}, {"preprod", domain.EnvStage},
		{"test", domain.EnvTest}, {" тест ", domain.EnvTest},
		{"dev", domain.EnvDev}, {"разработка", domain.EnvDev},
	} {
		got, err := domain.ParseEnv(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseEnv(%q) = %q, %v; нужно %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "прдо", "qa-2"} {
		if _, err := domain.ParseEnv(bad); !errors.Is(err, domain.ErrInvalidCredential) {
			t.Errorf("ParseEnv(%q) принят: %v", bad, err)
		}
	}
	if !domain.EnvProd.Risky() || domain.EnvTest.Risky() {
		t.Error("рискованная среда — только прод")
	}
}

func dto() domain.CredentialDTO {
	return domain.CredentialDTO{Product: " Склад-Про ", Env: domain.EnvProd, URL: "https://sklad.example.com", Host: "192.0.2.14",
		Login: "admin.ops", Role: "администратор", Rights: "все разделы", IssuedBy: "тимлид", IssuedAt: issued, Note: "только из офиса"}
}

// Карточка доступа — без пароля по устройству: пароль живет в Связке ключей.
// Id без явного — из продукта, среды и логина латиницей.
func TestNewCredential(t *testing.T) {
	c, err := domain.NewCredential(dto())
	if err != nil {
		t.Fatal(err)
	}
	if c.ID() != "sklad-pro-prod-admin-ops" || c.Product() != "Склад-Про" || c.Env() != domain.EnvProd || c.Login() != "admin.ops" ||
		c.URL() != "https://sklad.example.com" || c.Host() != "192.0.2.14" || !c.IssuedAt().Equal(issued) {
		t.Fatalf("карточка = %+v", c.DTO())
	}
	d := dto()
	d.ID = "Mine-1"
	if c, err := domain.NewCredential(d); err != nil || c.ID() != "mine-1" {
		t.Fatalf("свой id = %q, %v", c.ID(), err)
	}
	if back, err := domain.NewCredential(c.DTO()); err != nil || back.DTO() != c.DTO() {
		t.Fatalf("DTO туда-обратно: %+v, %v", back.DTO(), err)
	}
	for _, x := range []struct {
		name string
		edit func(*domain.CredentialDTO)
	}{
		{"без продукта", func(d *domain.CredentialDTO) { d.Product = "  " }},
		{"без логина", func(d *domain.CredentialDTO) { d.Login = "" }},
		{"без среды", func(d *domain.CredentialDTO) { d.Env = "" }},
		{"чужая среда", func(d *domain.CredentialDTO) { d.Env = "qa" }},
		{"ни адреса, ни хоста", func(d *domain.CredentialDTO) { d.URL, d.Host = "", "" }},
		{"id с пробелом", func(d *domain.CredentialDTO) { d.ID = "a b" }},
		{"id слишком длинный", func(d *domain.CredentialDTO) { d.ID = strings.Repeat("a", 65) }},
	} {
		d := dto()
		x.edit(&d)
		if _, err := domain.NewCredential(d); !errors.Is(err, domain.ErrInvalidCredential) {
			t.Errorf("%s: принята (%v)", x.name, err)
		}
	}
}

// Поиск по карточке: все слова запроса где-то в карточке, без регистра;
// среда находится и по-русски.
func TestCredential_Matches(t *testing.T) {
	c, _ := domain.NewCredential(dto())
	for q, want := range map[string]bool{
		"": true, "склад": true, "СКЛАД прод": true, "склад prod": true, "admin": true, "192.0.2": true, "администратор": true,
		"склад тест": false, "портал": false,
		// заметку поиск не смотрит: туда пишут что угодно, и число найденного
		// у агента выдавало бы ее содержимое по подстроке
		"офиса": false, "только": false,
	} {
		if got := c.Matches(q); got != want {
			t.Errorf("Matches(%q) = %v", q, got)
		}
	}
}

// Пароль — тип, а не строка: пустой — отказ; печать (%v, %+v, %#v, %s)
// пароля не раскрывает — случайный fmt в логе или ошибке его не выдаст.
func TestNewPassword(t *testing.T) {
	for _, bad := range []string{"", "   ", "\n"} {
		if _, err := domain.NewPassword(bad); !errors.Is(err, domain.ErrInvalidCredential) {
			t.Errorf("пароль %q принят: %v", bad, err)
		}
	}
	p, err := domain.NewPassword("Kq7!vR2m")
	if err != nil || p.Reveal() != "Kq7!vR2m" {
		t.Fatalf("пароль: %v", err)
	}
	for _, s := range []string{fmt.Sprint(p), fmt.Sprintf("%v %+v %#v %s %q", p, p, p, p, p), fmt.Sprint(struct{ P domain.Password }{p}),
		fmt.Sprintf("%+v %#v", struct{ p domain.Password }{p}, struct{ p domain.Password }{p}), // неэкспортируемое поле fmt читает отражением, мимо Format
		fmt.Sprintf("%v", []domain.Password{p}), fmt.Sprintf("%x %d", p, p)} {
		if strings.Contains(s, "Kq7") {
			t.Fatalf("печать раскрыла пароль: %s", s)
		}
	}
}
