// Package fileindexgob keeps the file index on disk in encoding/gob — the
// standard library, no database. Запись атомарная (временный файл и rename),
// права 0600: в индексе пути и слова из личных файлов.
package fileindexgob

import (
	"bufio"
	"encoding/gob"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// Store reads and writes one snapshot file.
type Store struct {
	Path string
}

// Load returns the stored snapshot. A missing, corrupt or other-version file
// reports false without an error: индекс соберется заново целиком, а падать из-за
// кэша значило бы не открыть окно вовсе.
func (s Store) Load() (filesearch.Snapshot, bool, error) {
	f, err := os.Open(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return filesearch.Snapshot{}, false, nil
	}
	if err != nil {
		return filesearch.Snapshot{}, false, err
	}
	defer func() { _ = f.Close() }() // только чтение
	var snap filesearch.Snapshot
	if err := gob.NewDecoder(bufio.NewReader(f)).Decode(&snap); err != nil {
		return filesearch.Snapshot{}, false, nil
	}
	if snap.Version != filesearch.SnapshotVersion {
		return filesearch.Snapshot{}, false, nil
	}
	return snap, true, nil
}

// Save writes the snapshot atomically: an interrupted write leaves the
// previous file intact.
func (s Store) Save(snap filesearch.Snapshot) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".index-*.gob") // CreateTemp создает с 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после rename файла уже нет
	w := bufio.NewWriter(tmp)
	if err := gob.NewEncoder(w).Encode(snap); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode index: %w", err)
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
