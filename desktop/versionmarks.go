package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// versionMark — что владелец отметил у версии: заметка и значок «актуальная».
// Файлы документов не трогаются: отметки лежат рядом с config.json.
type versionMark struct {
	Note    string `json:"note,omitempty"`
	Current bool   `json:"current,omitempty"`
}

func (m versionMark) empty() bool { return m.Note == "" && !m.Current }

// loadMarks reads the marks file; a missing or broken file is an empty set:
// заметки не стоят того, чтобы окно не открыло список документов.
func loadMarks(path string) map[string]versionMark {
	out := map[string]versionMark{}
	if path == "" {
		return out
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &out) != nil || out == nil {
		return map[string]versionMark{}
	}
	return out
}

// saveMarks writes the marks atomically, 0600: заметки — личное.
//
// ponytail: отметки удаленных файлов остаются в файле. Потолок: тысячи
// отметок за годы, это килобайты. Путь: чистка по списку хешей живого обхода.
func saveMarks(path string, marks map[string]versionMark) error {
	if path == "" {
		return errors.New("файл заметок не задан")
	}
	b, err := json.Marshal(marks)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "version-marks.*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после Rename файла уже нет
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path) // CreateTemp создает 0600
}

// markKey — под каким ключом лежит отметка версии: хеш содержимого, а значит
// переименование и перенос файла ее не теряют, а копия делит ее с оригиналом.
// Файл без хеша (слишком большой) отмечается по пути.
func markKey(v versionView) string {
	if v.hash != "" {
		return v.hash
	}
	return "path:" + v.Path
}
