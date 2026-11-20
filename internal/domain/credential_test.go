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

// Ключ (#712) — карточка без адреса и хоста: «ключ на чтение» к продукту не
// ведет никуда, он просто лежит. Название ключа занимает место логина, поэтому
// два ключа к одному продукту и среде различаются названием. Учетка без адреса
// по-прежнему отказ, а вид по умолчанию — учетка: старые файлы читаются как были.
func TestNewCredential_key(t *testing.T) {
	k, err := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: "Мой склад", Env: domain.EnvProd, Login: "ключ на чтение", Rights: "чтение"})
	if err != nil {
		t.Fatalf("ключ без адреса и хоста отвергнут: %v", err)
	}
	if k.Kind() != domain.KindKey || k.ID() != "moi-sklad-prod-key-klyuch-na-chtenie" {
		t.Fatalf("ключ = %+v", k.DTO())
	}
	other, err := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: "Мой склад", Env: domain.EnvProd, Login: "ключ на запись"})
	if err != nil || other.ID() == k.ID() {
		t.Fatalf("два ключа к одному продукту слились: %v %q", err, other.ID())
	}
	if _, err := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: "Мой склад", Env: domain.EnvProd}); !errors.Is(err, domain.ErrInvalidCredential) {
		t.Errorf("ключ без названия принят: %v", err)
	}
	acc := domain.CredentialDTO{Product: "Мой склад", Env: domain.EnvProd, Login: "admin"}
	if _, err := domain.NewCredential(acc); !errors.Is(err, domain.ErrInvalidCredential) {
		t.Errorf("учетка без адреса принята: %v", err)
	}
	acc.Host = "192.0.2.1"
	a, err := domain.NewCredential(acc)
	if err != nil || a.Kind() != domain.KindAccount {
		t.Errorf("пустой вид должен читаться как учетка: %v %q", err, a.Kind())
	}
	if _, err := domain.NewCredential(domain.CredentialDTO{Kind: "token", Product: "p", Env: domain.EnvDev, Login: "l", Host: "h"}); !errors.Is(err, domain.ErrInvalidCredential) {
		t.Errorf("незнакомый вид принят: %v", err)
	}
	if !k.Matches("ключ склад прод") {
		t.Error("поиск не находит ключ по слову «ключ»")
	}
}

// Ревью #714: учетка admin и ключ с названием admin к одному продукту и среде
// не должны получать один id, иначе добавление второй карточки отвечает
// «такой доступ уже есть» про карточку с другой вкладки.
func TestNewCredential_keyIDDiffersFromAccountID(t *testing.T) {
	acc, err := domain.NewCredential(domain.CredentialDTO{Product: "Склад", Env: domain.EnvProd, Login: "admin", Host: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: "Склад", Env: domain.EnvProd, Login: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID() == key.ID() {
		t.Fatalf("id учетки и ключа совпали: %q", acc.ID())
	}
	// Правка ключа пересчитывает собранный id тем же правилом, а не превращает его в учеточный.
	rev, err := key.Revise(domain.CredentialDTO{Login: "reader"})
	if err != nil || rev.ID() != "sklad-prod-key-reader" {
		t.Fatalf("после правки id = %q, %v", rev.ID(), err)
	}
}

// Ревью #714: прежний тест искал ключ по слову «ключ», которое стояло в самом
// названии, и оставался зеленым без kindSay. Здесь название слова не содержит, а
// учетка по слову «ключ» находиться не должна.
func TestCredential_matchesKeyByKindWord(t *testing.T) {
	key, err := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: "Склад", Env: domain.EnvProd, Login: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	acc, err := domain.NewCredential(domain.CredentialDTO{Product: "Склад", Env: domain.EnvProd, Login: "admin", Host: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !key.Matches("склад ключ") {
		t.Error("ключ read-only не найден по слову «ключ»")
	}
	if acc.Matches("склад ключ") {
		t.Error("учетка найдена по слову «ключ»")
	}
}

// Живая проверка 20.11: продукт «Токен облачной сессии для Claude» и ключ с тем же названием
// дали id в 78 знаков, и окно отвечало «latinica, цифры и дефис, до 64 знаков», хотя человек
// ввел ничего неправильного. Длинный собранный id укорачивается с хешем полного имени:
// остается допустимым, предсказуемым и различимым у двух длинных имен с общим началом.
func TestNewCredential_longNamesGetAValidID(t *testing.T) {
	const name = "Токен облачной сессии для Claude"
	for _, kind := range []domain.CredentialKind{domain.KindKey, domain.KindAccount} {
		c, err := domain.NewCredential(domain.CredentialDTO{Kind: kind, Product: name, Env: domain.EnvProd, Login: name, Host: "192.0.2.1"})
		if err != nil {
			t.Fatalf("%s: длинное имя отвергнуто: %v", kind, err)
		}
		if len(c.ID()) > 64 {
			t.Errorf("%s: id %q длиннее 64 знаков (%d)", kind, c.ID(), len(c.ID()))
		}
		again, _ := domain.NewCredential(domain.CredentialDTO{Kind: kind, Product: name, Env: domain.EnvProd, Login: name, Host: "192.0.2.1"})
		if again.ID() != c.ID() {
			t.Errorf("%s: id не повторяется: %q и %q", kind, c.ID(), again.ID())
		}
	}
	a, errA := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: name, Env: domain.EnvProd, Login: name + " на чтение"})
	b, errB := domain.NewCredential(domain.CredentialDTO{Kind: domain.KindKey, Product: name, Env: domain.EnvProd, Login: name + " на запись"})
	if errA != nil || errB != nil || a.ID() == b.ID() {
		t.Errorf("два длинных ключа с общим началом слились: %q %q (%v %v)", a.ID(), b.ID(), errA, errB)
	}
	// Короткие имена остаются как были: уже сохраненные карточки находятся по прежним id.
	short, _ := domain.NewCredential(domain.CredentialDTO{Product: "Склад-Про", Env: domain.EnvProd, Login: "admin.ops", Host: "h"})
	if short.ID() != "sklad-pro-prod-admin-ops" {
		t.Errorf("короткий id изменился: %q", short.ID())
	}
}
