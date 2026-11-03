// Package extractcache keeps text extracted from PDFs and images (by the
// kb-extract helper) on disk, so that every process reading the folders — the
// window, MCP sessions — gets it without running the extraction again. Только
// стандартная библиотека; запись атомарная, права 0600, как у index.gob.
package extractcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cache stores one text per file version. Нулевое значение — кеша нет.
//
// ponytail: записи старых версий не удаляются — измененный файл оставляет
// прежнюю запись. Потолок: тысячи правок распознаваемых файлов (запись —
// килобайты текста). Путь: при проходе окна удалять записи, не задетые обходом.
type Cache struct {
	Dir string
}

// key names the entry for one version of a file. Путь хешируется: имя записи
// не должно выдавать, какие файлы лежат на диске.
func key(path string, size int64, mod time.Time) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d\x00%d", path, size, mod.Unix(), mod.Nanosecond()))
	return hex.EncodeToString(sum[:16]) + ".txt"
}

// Text returns the stored text of this version of the file. Пустой текст —
// тоже попадание: картинка без надписей уже распознана.
func (c Cache) Text(path string, size int64, mod time.Time) (string, bool) {
	if c.Dir == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(c.Dir, key(path, size, mod)))
	if err != nil {
		return "", false // нет записи или не читается — распознать заново
	}
	return string(b), true
}

// Put stores the text of this version of the file.
func (c Cache) Put(path string, size int64, mod time.Time, text string) error {
	if c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, ".put-*") // CreateTemp создает с 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после rename файла уже нет
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(c.Dir, key(path, size, mod)))
}
