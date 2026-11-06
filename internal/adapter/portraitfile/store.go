// Package portraitfile keeps the portrait (#479) as portrait.json in a folder
// the person chose: только для его пользователя macOS (0600), запись атомарная.
package portraitfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Name is the portrait file name inside the folder.
const Name = "portrait.json"

// ErrNoDir — папка портрета не выбрана.
var ErrNoDir = errors.New("portrait folder is not set")

// ErrCorrupt — файл портрета не читается: перезаписывать его пустым нельзя.
var ErrCorrupt = errors.New("portrait file is corrupt")

// Store reads and writes Dir/portrait.json.
type Store struct {
	Dir string
}

type fileFact struct {
	ID      string    `json:"id"`
	Section string    `json:"section"`
	Text    string    `json:"text"`
	Source  string    `json:"source,omitempty"`
	Status  string    `json:"status"`
	At      time.Time `json:"at"`
}

type file struct {
	Facts []fileFact `json:"facts"`
}

func (s Store) path() (string, error) {
	if s.Dir == "" {
		return "", ErrNoDir
	}
	return filepath.Join(s.Dir, Name), nil
}

// Load reads the portrait; файла еще нет — пустой портрет.
func (s Store) Load(_ context.Context) (domain.Portrait, error) {
	path, err := s.path()
	if err != nil {
		return domain.Portrait{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return domain.NewPortrait(nil)
	}
	if err != nil {
		return domain.Portrait{}, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return domain.Portrait{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	facts := make([]domain.Fact, 0, len(f.Facts))
	for _, x := range f.Facts {
		fact, err := domain.NewFact(x.ID, domain.FactSection(x.Section), x.Text, x.Source, domain.FactStatus(x.Status), x.At)
		if err != nil {
			return domain.Portrait{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
		}
		facts = append(facts, fact)
	}
	p, err := domain.NewPortrait(facts)
	if err != nil {
		return domain.Portrait{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return p, nil
}

// Save writes the portrait: во временный файл рядом, затем переименование —
// оборванная запись не оставляет полфайла.
func (s Store) Save(_ context.Context, p domain.Portrait) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	var f file
	for _, x := range p.Facts() {
		f.Facts = append(f.Facts, fileFact{x.ID(), string(x.Section()), x.Text(), x.Source(), string(x.Status()), x.At()})
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".portrait-*.json") // CreateTemp — 0600
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
