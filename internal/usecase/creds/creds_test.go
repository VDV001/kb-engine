package creds_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/creds"
)

// cardsMem keeps cards in memory; failSave — запись карточек отказывает.
type cardsMem struct {
	list     []domain.Credential
	failSave bool
}

func (m *cardsMem) Update(fn func([]domain.Credential) ([]domain.Credential, error)) error {
	next, err := fn(slices.Clone(m.list))
	if err != nil {
		return err
	}
	if m.failSave {
		return errors.New("диск полон")
	}
	m.list = next
	return nil
}

func (m *cardsMem) Load() ([]domain.Credential, error) { return slices.Clone(m.list), nil }

// secretsMem is the keychain; deleted — что откат снял; failDelete — снять не выходит.
type secretsMem struct {
	m          map[string]string
	deleted    []string
	failDelete bool
}

func (s *secretsMem) Has(account string) (bool, error) { _, ok := s.m[account]; return ok, nil }

func (s *secretsMem) Set(account, secret string) error { s.m[account] = secret; return nil }
func (s *secretsMem) Get(account string) (string, error) {
	v, ok := s.m[account]
	if !ok {
		return "", errors.New("нет записи")
	}
	return v, nil
}
func (s *secretsMem) Delete(account string) error {
	if s.failDelete {
		return errors.New("связка заперта")
	}
	delete(s.m, account)
	s.deleted = append(s.deleted, account)
	return nil
}

func card(t *testing.T, product string, env domain.Env, login string) domain.Credential {
	t.Helper()
	c, err := domain.NewCredential(domain.CredentialDTO{Product: product, Env: env, Login: login, Host: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func pw(t *testing.T, s string) domain.Password {
	t.Helper()
	p, err := domain.NewPassword(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newSvc() (creds.Service, *cardsMem, *secretsMem) {
	c, s := &cardsMem{}, &secretsMem{m: map[string]string{}}
	return creds.Service{Cards: c, Secrets: s}, c, s
}

// Добавить: пароль — в Связку, карточка — в файл; второй с тем же id — отказ,
// и чужой пароль не перезаписан; без пароля — отказ.
func TestService_Add(t *testing.T) {
	svc, cards, secrets := newSvc()
	a := card(t, "Склад-Про", domain.EnvProd, "admin")
	if err := svc.Add(a, pw(t, "s3cret")); err != nil {
		t.Fatal(err)
	}
	if len(cards.list) != 1 || secrets.m[a.ID()] != "s3cret" {
		t.Fatalf("карточек %d, пароль %q", len(cards.list), secrets.m[a.ID()])
	}
	if err := svc.Add(a, pw(t, "other")); !errors.Is(err, domain.ErrCredentialExists) {
		t.Fatalf("повтор принят: %v", err)
	}
	if secrets.m[a.ID()] != "s3cret" {
		t.Fatalf("повтор перезаписал пароль: %q", secrets.m[a.ID()])
	}
}

// Карточка не записалась — откат снимает только свой пароль, а не чужой с тем
// же именем (урок 05.11: откат должен знать, что создал).
func TestService_Add_rollsBackOwnSecret(t *testing.T) {
	svc, cards, secrets := newSvc()
	a := card(t, "Склад-Про", domain.EnvProd, "admin")
	cards.failSave = true
	if err := svc.Add(a, pw(t, "s3cret")); err == nil {
		t.Fatal("ошибка записи карточки потеряна")
	}
	if _, ok := secrets.m[a.ID()]; ok || !slices.Equal(secrets.deleted, []string{a.ID()}) {
		t.Fatalf("откат: осталось %v, снято %v", secrets.m, secrets.deleted)
	}
}

// Найти: все слова запроса, порядок — продукт, среда, логин; пароля в ответе
// нет по типу (карточка).
func TestService_Find(t *testing.T) {
	svc, cards, _ := newSvc()
	cards.list = []domain.Credential{
		card(t, "Склад-Про", domain.EnvTest, "admin"),
		card(t, "CRM Север", domain.EnvTest, "api"),
		card(t, "Склад-Про", domain.EnvProd, "viewer"),
		card(t, "Склад-Про", domain.EnvProd, "admin"),
	}
	got, err := svc.Find("склад")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID())
	}
	want := []string{"sklad-pro-prod-admin", "sklad-pro-prod-viewer", "sklad-pro-test-admin"}
	if !slices.Equal(ids, want) {
		t.Fatalf("нашлось %v, нужно %v", ids, want)
	}
}

// Пароль — только по id существующей карточки.
func TestService_Password(t *testing.T) {
	svc, _, _ := newSvc()
	a := card(t, "Склад-Про", domain.EnvProd, "admin")
	if err := svc.Add(a, pw(t, "s3cret")); err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Password(a.ID()); err != nil || p != "s3cret" {
		t.Fatalf("пароль %q, %v", p, err)
	}
	if _, err := svc.Password("nope"); !errors.Is(err, domain.ErrCredentialNotFound) {
		t.Fatalf("чужой id: %v", err)
	}
}

// Связка одна на все файлы карточек: пароль с этим id уже лежит (сирота,
// карточка из другого --file) — отказ, чужой пароль не перезаписан и не снят.
func TestService_Add_refusesForeignSecret(t *testing.T) {
	svc, cards, secrets := newSvc()
	a := card(t, "Склад-Про", domain.EnvProd, "admin")
	secrets.m[a.ID()] = "чужой"
	if err := svc.Add(a, pw(t, "s3cret")); !errors.Is(err, domain.ErrCredentialExists) {
		t.Fatalf("чужой пароль затерт: %v", err)
	}
	if secrets.m[a.ID()] != "чужой" || len(secrets.deleted) != 0 || len(cards.list) != 0 {
		t.Fatalf("Связка %v, снято %v, карточек %d", secrets.m, secrets.deleted, len(cards.list))
	}
}

// Откат не смог снять свой пароль — ошибка говорит, какой остался в Связке.
func TestService_Add_rollbackFailureIsReported(t *testing.T) {
	svc, cards, secrets := newSvc()
	a := card(t, "Склад-Про", domain.EnvProd, "admin")
	cards.failSave, secrets.failDelete = true, true
	err := svc.Add(a, pw(t, "s3cret"))
	if err == nil || !strings.Contains(err.Error(), a.ID()) || !strings.Contains(err.Error(), "остался") {
		t.Fatalf("ошибка отката: %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatal("пароль в тексте ошибки")
	}
}
