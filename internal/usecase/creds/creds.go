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

// Update changes the card id and, when password is not nil, its password. Пустые
// продукт, среда и логин в details значат «не менять»; исправленные меняют адрес
// карточки, и пароль переезжает в Связке под новый адрес, старый снимается.
// Пароль меняется первым, прежний запоминается: не записалась карточка — все
// возвращается, иначе доступ остался бы с паролем, которого нет в карточке.
// Нет такой карточки, правка невалидна или новый адрес занят — ничего не тронуто.
func (s Service) Update(id string, details domain.CredentialDTO, password *domain.Password) error {
	var old, movedTo string
	var hadOld, changed bool
	err := s.Cards.Update(func(list []domain.Credential) ([]domain.Credential, error) {
		i := slices.IndexFunc(list, func(c domain.Credential) bool { return c.ID() == id })
		if i < 0 {
			return nil, fmt.Errorf("%w: %s", domain.ErrCredentialNotFound, id)
		}
		revised, err := list[i].Revise(details)
		if err != nil {
			return nil, err
		}
		newID := revised.ID()
		if newID != id {
			if slices.ContainsFunc(list, func(c domain.Credential) bool { return c.ID() == newID }) {
				return nil, fmt.Errorf("%w: %s", domain.ErrCredentialExists, newID)
			}
			switch has, err := s.Secrets.Has(newID); {
			case err != nil:
				return nil, fmt.Errorf("creds: Связка ключей не ответила: %w", err)
			case has:
				return nil, fmt.Errorf("%w: пароль %s уже лежит в Связке ключей", domain.ErrCredentialExists, newID)
			}
		}
		if password != nil || newID != id {
			cur, err := s.Secrets.Get(id)
			if err != nil {
				return nil, fmt.Errorf("creds: прежний пароль не прочитан, правка отменена: %w", err)
			}
			old, hadOld = cur, true
			next := cur
			if password != nil {
				next = password.Reveal()
			}
			if err := s.Secrets.Set(newID, next); err != nil {
				return nil, fmt.Errorf("creds: пароль не лег в Связку ключей: %w", err)
			}
			changed = true
			if newID != id {
				movedTo = newID
			}
		}
		list[i] = revised
		return list, nil
	})
	if err != nil {
		if !changed {
			return err
		}
		if movedTo != "" {
			if derr := s.Secrets.Delete(movedTo); derr != nil {
				err = errors.Join(err, fmt.Errorf("creds: пароль %s остался в Связке ключей: %w", movedTo, derr))
			}
		} else if hadOld {
			if rerr := s.Secrets.Set(id, old); rerr != nil {
				err = errors.Join(err, fmt.Errorf("creds: прежний пароль %s не возвращен в Связку ключей: %w", id, rerr))
			}
		}
		return err
	}
	if movedTo != "" {
		if derr := s.Secrets.Delete(id); derr != nil {
			return fmt.Errorf("creds: карточка сохранена под новым адресом, но старый пароль %s остался в Связке ключей: %w", id, derr)
		}
	}
	return nil
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

// Delete removes the access: сначала пароль из Связки, потом карточку. Связка
// не отдала пароль — карточка остается: без нее пароль стал бы сиротой,
// которую не найти ни поиском, ни окном.
func (s Service) Delete(id string) error {
	removed := false
	err := s.Cards.Update(func(list []domain.Credential) ([]domain.Credential, error) {
		i := slices.IndexFunc(list, func(c domain.Credential) bool { return c.ID() == id })
		if i < 0 {
			return nil, fmt.Errorf("%w: %s", domain.ErrCredentialNotFound, id)
		}
		if err := s.Secrets.Delete(id); err != nil {
			return nil, fmt.Errorf("creds: пароль %s не снят из Связки ключей, карточка оставлена: %w", id, err)
		}
		removed = true
		return slices.Delete(list, i, i+1), nil
	})
	if err != nil && removed {
		// Пароль уже ушел, а карточка осталась: сказать, как есть, и как довести —
		// повторный rm снимет карточку (Связка «нет записи» не считает ошибкой).
		return fmt.Errorf("creds: %s — пароль снят из Связки, а карточку записать не вышло, повторите удаление: %w", id, err)
	}
	return err
}
