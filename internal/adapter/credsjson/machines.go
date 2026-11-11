package credsjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/domain"
)

// MachineDefaultPath is the machines file next to the access cards: окно,
// терминал и MCP видят одни машины.
func MachineDefaultPath(home string) string {
	return filepath.Join(filepath.Dir(DefaultPath(home)), "machines.json")
}

// MachineStore keeps machine cards (#549) as one JSON file, только для своего
// пользователя (0600), без паролей: пароль в Связке ключей, в файле путь ключа
// и подсказка про токен, но не секрет.
type MachineStore struct {
	Path string
}

// Load reads every machine; файла нет — пусто. Битый файл или карточка,
// нарушающая инвариант, — ошибка с путем, а не пустой список: пустой список на
// записи затер бы все машины.
func (s MachineStore) Load() ([]domain.Machine, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("машины: не прочитать %s: %w", s.Path, err)
	}
	var dtos []domain.MachineDTO
	if err := json.Unmarshal(raw, &dtos); err != nil {
		return nil, fmt.Errorf("машины: %s испорчен: %w", s.Path, err)
	}
	out := make([]domain.Machine, 0, len(dtos))
	for _, d := range dtos {
		m, err := domain.NewMachine(d)
		if err != nil {
			return nil, fmt.Errorf("машины: %s, карточка %q: %w", s.Path, d.ID, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// Update reads, changes and writes the machines under the file lock; ошибка fn
// оставляет файл как был. Запись атомарная.
func (s MachineStore) Update(fn func([]domain.Machine) ([]domain.Machine, error)) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("машины: не создать каталог: %w", err)
	}
	return filelock.With(s.Path, func() error {
		list, err := s.Load()
		if err != nil {
			return err
		}
		if list, err = fn(list); err != nil {
			return err
		}
		dtos := make([]domain.MachineDTO, 0, len(list))
		for _, m := range list {
			dtos = append(dtos, m.DTO())
		}
		raw, err := json.MarshalIndent(dtos, "", "  ")
		if err != nil {
			return fmt.Errorf("машины: не закодировать: %w", err)
		}
		return writeAtomic(s.Path, ".machines-*.tmp", append(raw, '\n'))
	})
}
