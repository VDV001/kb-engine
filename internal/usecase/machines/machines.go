// Package machines keeps machine cards (#549): сервер, на котором лежит много
// проектов, со способом входа и цепочкой через бастион. Карточку видят поиск
// окна, терминал и агент; пароль и путь ключа агенту не уходят (domain.Machine).
package machines

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Store keeps machine cards; Update — чтение и запись под одним замком, иначе
// два одновременных «добавить» потеряли бы машину.
type Store interface {
	Load() ([]domain.Machine, error)
	Update(fn func([]domain.Machine) ([]domain.Machine, error)) error
}

// Service adds, finds, changes and opens machine cards.
type Service struct {
	Machines Store
}

func indexOf(list []domain.Machine, id string) int {
	return slices.IndexFunc(list, func(m domain.Machine) bool { return m.ID() == id })
}

// Add stores the machine; машина с тем же id — отказ, список не тронут.
// Бастион может быть заведен позже: разрыв цепочки называется при Command.
func (s Service) Add(m domain.Machine) error {
	return s.Machines.Update(func(list []domain.Machine) ([]domain.Machine, error) {
		if indexOf(list, m.ID()) >= 0 {
			return nil, fmt.Errorf("%w: %s", domain.ErrMachineExists, m.ID())
		}
		return append(list, m), nil
	})
}

// Update replaces the details of a machine; id остается тем, что адресован, а не
// тем, что в правке. Невалидная правка или нет такой машины — ничего не тронуто.
func (s Service) Update(id string, details domain.MachineDTO) error {
	return s.Machines.Update(func(list []domain.Machine) ([]domain.Machine, error) {
		i := indexOf(list, id)
		if i < 0 {
			return nil, fmt.Errorf("%w: %s", domain.ErrMachineNotFound, id)
		}
		details.ID = id
		revised, err := domain.NewMachine(details)
		if err != nil {
			return nil, err
		}
		list[i] = revised
		return list, nil
	})
}

// Delete removes the machine. Бастион удалить можно: цепочки, которые через него
// шли, покажут разрыв при Command, а не сломаются молча.
func (s Service) Delete(id string) error {
	return s.Machines.Update(func(list []domain.Machine) ([]domain.Machine, error) {
		i := indexOf(list, id)
		if i < 0 {
			return nil, fmt.Errorf("%w: %s", domain.ErrMachineNotFound, id)
		}
		return slices.Delete(list, i, i+1), nil
	})
}

// Find returns the machines whose card has every word of q; пустой запрос —
// все. Порядок один для окна и агента: по группе, потом по имени.
func (s Service) Find(q string) ([]domain.Machine, error) {
	list, err := s.Machines.Load()
	if err != nil {
		return nil, err
	}
	found := slices.DeleteFunc(list, func(m domain.Machine) bool { return !m.Matches(q) })
	slices.SortFunc(found, func(a, b domain.Machine) int {
		return cmp.Or(
			strings.Compare(strings.ToLower(a.Group()), strings.ToLower(b.Group())),
			strings.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name())),
		)
	})
	return found, nil
}

// Command builds the ssh command for a machine through its bastions.
func (s Service) Command(id string) (string, error) {
	list, err := s.Machines.Load()
	if err != nil {
		return "", err
	}
	i := indexOf(list, id)
	if i < 0 {
		return "", fmt.Errorf("%w: %s", domain.ErrMachineNotFound, id)
	}
	chain, err := domain.ResolveChain(list[i], func(id string) (domain.Machine, bool) {
		j := indexOf(list, id)
		if j < 0 {
			return domain.Machine{}, false
		}
		return list[j], true
	})
	if err != nil {
		return "", err
	}
	return domain.SSHCommand(chain), nil
}
