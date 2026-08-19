// Package searchsyn reads the search synonym dictionary that lives next to the
// catalog.
//
// Словарь — файл, а не таблица в коде, потому что его правит человек: список
// «как я спрашиваю → как это записано в базе» растёт от каждого промаха поиска,
// и требовать ради строки пересборку движка значило бы, что её никто не
// допишет. Тот же довод, что у словаря быстрого ввода в финансах.
package searchsyn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/usecase/search"
)

// FileName is the dictionary's name inside the catalog's directory.
const FileName = "search-synonyms.json"

// PathNextTo returns where the dictionary lives for a given catalog.
func PathNextTo(catalogPath string) string {
	return filepath.Join(filepath.Dir(catalogPath), FileName)
}

// ErrNoDictionary reports that the file is simply not there.
//
// Отдельная ошибка, а не пустой словарь: «перевода нет, потому что файла нет» и
// «перевод не сработал» — разные ответы, и вызывающий обязан их различать,
// иначе отсутствие слоя выглядит как его неудача.
var ErrNoDictionary = errors.New("searchsyn: словаря синонимов нет")

// Load reads the dictionary.
func Load(path string) (search.Dictionary, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoDictionary, path)
	}
	if err != nil {
		return nil, fmt.Errorf("searchsyn: не прочитать словарь: %w", err)
	}
	var d search.Dictionary
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("searchsyn: %s не разобрать: %w", path, err)
	}
	return d, nil
}
