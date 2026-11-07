// Package credsjson keeps access cards (#494) as one JSON file next to the
// window settings: только для своего пользователя (0600), без паролей — пароли
// в Связке ключей. Реализует creds.Cards.
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

// Store is the cards file at Path.
type Store struct {
	Path string
}

// Load reads every card; файла нет — пусто. Битый файл — ошибка с путем, а не
// пустой список: пустой список на записи затер бы все карточки.
func (s Store) Load() ([]domain.Credential, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("доступы: не прочитать %s: %w", s.Path, err)
	}
	var dtos []domain.CredentialDTO
	if err := json.Unmarshal(raw, &dtos); err != nil {
		return nil, fmt.Errorf("доступы: %s испорчен: %w", s.Path, err)
	}
	out := make([]domain.Credential, 0, len(dtos))
	for _, d := range dtos {
		c, err := domain.NewCredential(d)
		if err != nil {
			return nil, fmt.Errorf("доступы: %s, карточка %q: %w", s.Path, d.ID, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// Update reads, changes and writes the cards under the file lock; ошибка fn
// оставляет файл как был. Запись атомарная: временный файл 0600 и rename.
func (s Store) Update(fn func([]domain.Credential) ([]domain.Credential, error)) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("доступы: не создать каталог: %w", err)
	}
	return filelock.With(s.Path, func() error {
		list, err := s.Load()
		if err != nil {
			return err
		}
		if list, err = fn(list); err != nil {
			return err
		}
		dtos := make([]domain.CredentialDTO, 0, len(list))
		for _, c := range list {
			dtos = append(dtos, c.DTO())
		}
		raw, err := json.MarshalIndent(dtos, "", "  ")
		if err != nil {
			return fmt.Errorf("доступы: не закодировать: %w", err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".credentials-*.tmp") // CreateTemp создает с 0600
		if err != nil {
			return fmt.Errorf("доступы: не записать: %w", err)
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		if _, err := tmp.Write(append(raw, '\n')); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("доступы: не записать: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return fmt.Errorf("доступы: не записать: %w", err)
		}
		return os.Rename(tmp.Name(), s.Path)
	})
}
