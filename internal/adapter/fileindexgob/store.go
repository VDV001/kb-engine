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
	"time"

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
	removeStaleTemps(dir)
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
	// Sync до rename: без него после сбоя питания на месте index.gob может
	// оказаться пустой файл, и обещание «прерванная запись не портит прежний»
	// не держится (ревью #374).
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// staleTempAge — временные файлы старше часа оставил убитый процесс: живая
// запись столько не длится. Каждый такой файл весит как весь индекс.
const staleTempAge = time.Hour

func removeStaleTemps(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, ".index-*.gob"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && time.Since(info.ModTime()) > staleTempAge {
			_ = os.Remove(m) // не удалось — уберет следующая запись
		}
	}
}

// Option returns the service option that keeps the index at path; an empty
// path keeps it in memory only. Одна точка подключения для команд и окна:
// правила хранения не разойдутся (ревью #374).
func Option(path string) filesearch.Option {
	if path == "" {
		return filesearch.WithStore(nil)
	}
	return filesearch.WithStore(Store{Path: path})
}
