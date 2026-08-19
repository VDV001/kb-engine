// Package searchindex reads and writes the vector index that lives next to the
// catalog.
//
// Индекс — отдельный файл, а не часть каталога: он производный, весит на два
// порядка больше записей и пересобирается целиком. Держать его в catalog.json
// значило бы гонять мегабайты через каждую правку одной строки.
package searchindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/usecase/search"
)

// FileName is the index's name inside the catalog's directory.
const FileName = "search-index.json"

// PathNextTo returns where the index lives for a given catalog.
func PathNextTo(catalogPath string) string {
	return filepath.Join(filepath.Dir(catalogPath), FileName)
}

// ErrNoIndex reports that the file is simply not there.
var ErrNoIndex = errors.New("searchindex: индекса нет")

// file is the shape on disk.
//
// Имя модели и размерность лежат РЯДОМ с векторами: индекс, снятый другой
// моделью, состоит из таких же чисел и молча даёт правдоподобный шум. Поле
// built — когда снят, чтобы человек видел, насколько индекс отстал от базы.
type file struct {
	Model   string               `json:"model"`
	Dims    int                  `json:"dims"`
	Built   string               `json:"built"`
	Vectors map[string][]float32 `json:"vectors"`
}

// Load reads the index.
func Load(path string) (search.Index, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return search.Index{}, fmt.Errorf("%w: %s", ErrNoIndex, path)
	}
	if err != nil {
		return search.Index{}, fmt.Errorf("searchindex: не прочитать индекс: %w", err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return search.Index{}, fmt.Errorf("searchindex: %s не разобрать: %w", path, err)
	}
	ix := search.Index{Model: f.Model, Dims: f.Dims, Vectors: map[int]search.Vector{}}
	for k, v := range f.Vectors {
		id, err := parseID(k)
		if err != nil {
			// Нечитаемый ключ пропускается, а не рушит индекс целиком: одна
			// испорченная строка не повод остаться без поиска. Но и молчать
			// нельзя — счёт возвращается вызывающему.
			continue
		}
		ix.Vectors[id] = v
	}
	return ix, nil
}

func parseID(s string) (int, error) {
	var id int
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, err
	}
	return id, nil
}
