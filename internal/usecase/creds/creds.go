// Package creds keeps access cards and their passwords (#494): карточка —
// файлом, ее видят поиск, терминал и агент; пароль — в Связке ключей, и
// наружу он выходит только сюда, в Password, по прямому id.
package creds

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/daniil/kb-engine/internal/domain"
)

// Cards stores access cards; Update — чтение и запись под одним замком, иначе
// два одновременных «добавить» потеряли бы карточку.
type Cards interface {
	Load() ([]domain.Credential, error)
	Update(fn func([]domain.Credential) ([]domain.Credential, error)) error
}

// Secrets stores passwords by card id (Связка ключей). Связка одна на все
// файлы карточек, поэтому Has: запись с этим id может быть чужой.
type Secrets interface {
	Has(account string) (bool, error)
	Set(account, secret string) error
	Get(account string) (string, error)
	Delete(account string) error
}

// Service adds, finds and opens access cards.
type Service struct {
	Cards   Cards
	Secrets Secrets
}

// Add stores the card and its password. Карточка с тем же id или пароль с
// этим id уже в Связке (сирота, карточка другого файла) — отказ, чужое не
// тронуто. Не записалась карточка — снимается только пароль, положенный этим
// вызовом: до него записи с этим id в Связке не было.
//
// ponytail: «есть ли» и запись идут под замком своего файла карточек, а
// Связка одна на все файлы: две команды add с одним id и разными --file могут
// разминуться между Has и Set. Потолок — узкое окно, только при нескольких
// файлах (окно и терминал пишут один). Путь — замок на службу Связки, а не на файл.
func (s Service) Add(c domain.Credential, password domain.Password) error {
	placed := false
	err := s.Cards.Update(func(list []domain.Credential) ([]domain.Credential, error) {
		if slices.ContainsFunc(list, func(x domain.Credential) bool { return x.ID() == c.ID() }) {
			return nil, fmt.Errorf("%w: %s", domain.ErrCredentialExists, c.ID())
		}
		switch has, err := s.Secrets.Has(c.ID()); {
		case err != nil:
			return nil, fmt.Errorf("creds: Связка ключей не ответила: %w", err)
		case has:
			return nil, fmt.Errorf("%w: пароль %s уже лежит в Связке ключей", domain.ErrCredentialExists, c.ID())
		}
		if err := s.Secrets.Set(c.ID(), password.Reveal()); err != nil {
			return nil, fmt.Errorf("creds: пароль не лег в Связку ключей: %w", err)
		}
		placed = true
		return append(list, c), nil
	})
	if err != nil && placed {
		if derr := s.Secrets.Delete(c.ID()); derr != nil {
			return errors.Join(err, fmt.Errorf("creds: пароль %s остался в Связке ключей: %w", c.ID(), derr))
		}
	}
	return err
}

// Find returns the cards with every word of q, по продукту, среде и логину.
func (s Service) Find(q string) ([]domain.Credential, error) {
	list, err := s.Cards.Load()
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(c domain.Credential) bool { return !c.Matches(q) })
	slices.SortFunc(list, func(a, b domain.Credential) int {
		return cmp.Or(cmp.Compare(a.Product(), b.Product()), cmp.Compare(a.Env(), b.Env()), cmp.Compare(a.Login(), b.Login()))
	})
	return list, nil
}

// Password returns the password of the card id. Звать только оттуда, где его
// видит человек сам (буфер, окно по Touch ID), — никогда из MCP.
func (s Service) Password(id string) (string, error) {
	list, err := s.Cards.Load()
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(list, func(c domain.Credential) bool { return c.ID() == id }) {
		return "", fmt.Errorf("%w: %s", domain.ErrCredentialNotFound, id)
	}
	return s.Secrets.Get(id)
}
