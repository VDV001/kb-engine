// Package portraitfile keeps the portrait (#479) as portrait.json in a folder
// the person chose: только для его пользователя macOS (0600), запись атомарная.
package portraitfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/adapter/portraitjson"
	"github.com/daniil/kb-engine/internal/domain"
)

// Name is the portrait file name inside the folder.
const Name = "portrait.json"

// ErrNoDir — папка портрета не выбрана.
var ErrNoDir = errors.New("portrait folder is not set")

// ErrCorrupt — файл портрета не читается: перезаписывать его пустым нельзя.
var ErrCorrupt = portraitjson.ErrCorrupt

// Store reads and writes Dir/portrait.json.
type Store struct {
	Dir string
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
	return portraitjson.Decode(raw)
}

// Save writes the portrait: во временный файл рядом, затем переименование —
// оборванная запись не оставляет полфайла.
func (s Store) Save(_ context.Context, p domain.Portrait) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	raw, err := portraitjson.Encode(p)
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
