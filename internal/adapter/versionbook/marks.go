package versionbook

import (
	"encoding/json"
	"errors"
	"io/fs"
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

// loadMarks reads the marks file. Нет файла — пустой набор. Битый файл
// откладывается рядом как .broken, и набор начинается заново: заметки не стоят
// того, чтобы окно не открыло список. Нечитаемый файл (права, диск) — ошибка:
// первая же новая запись иначе затерла бы целый файл.
func loadMarks(path string) (map[string]versionMark, error) {
	out := map[string]versionMark{}
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		if err := os.Rename(path, path+".broken"); err != nil {
			return map[string]versionMark{}, err
		}
		return map[string]versionMark{}, nil
	}
	return out, nil
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
//
// ponytail: одинаковое содержимое в двух семействах делит заметку и значок
// (копия под другим именем — это тот же документ по содержимому). Потолок:
// «актуальная» у двух семейств сразу. Путь: ключ из хеша и имени семейства.
func markKey(v VersionView) string {
	if v.hash != "" {
		return v.hash
	}
	return "path:" + v.Path
}
