// Package searchsyn reads the search synonym dictionary that lives next to the
// catalog.
//
// Словарь — файл, а не таблица в коде, потому что его правит человек: список
// «как я спрашиваю → как это записано в базе» растёт от каждого промаха поиска,
// и требовать ради строки пересборку движка значило бы, что её никто не
// допишет. Тот же довод, что у словаря быстрого ввода в финансах.
package searchsyn

import (
	"bytes"
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
	var byKey map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byKey); err != nil {
		return nil, fmt.Errorf("searchsyn: %s не разобрать: %w", path, err)
	}
	d := make(search.Dictionary, len(byKey))
	for k, raw := range byKey {
		t, err := terms(raw)
		if err != nil {
			return nil, fmt.Errorf("searchsyn: %s, ключ %q: %w", path, k, err)
		}
		d[k] = t
	}
	return d, nil
}

// terms читает одну строку словаря в любой из двух форм.
//
// Короткая — просто список равнозначных написаний, как файл выглядел всегда.
// Полная разводит две разные связи: same работает в обе стороны, includes
// только от темы к её содержимому. Разделение пришло замером, а не вкусом:
// пока redis лежал равнозначным кешированию, запрос «redis» отдавал 26 записей
// при семи, где это слово есть.
//
// Обе формы читаются одним проходом, потому что переписывать существующий файл
// ради нового поля значило бы оставить перевод терминов сломанным у всех, кто
// не сделал этого в ту же минуту.
func terms(raw json.RawMessage) (search.Terms, error) {
	var same []string
	if err := json.Unmarshal(raw, &same); err == nil {
		return search.Terms{Same: same}, nil
	}
	var full struct {
		Same     []string `json:"same"`
		Includes []string `json:"includes"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // опечатка в поле — молчаливо потерянный список
	if err := dec.Decode(&full); err != nil {
		return search.Terms{}, fmt.Errorf("ожидался список написаний или объект {same, includes}: %w", err)
	}
	return search.Terms{Same: full.Same, Includes: full.Includes}, nil
}
