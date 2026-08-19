package search

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// Vector — представление текста числами, снятое моделью.
//
// float32, а не float64: индекс каталога на полторы тысячи записей при 768
// измерениях — это 4,5 МБ против девяти, а точность косинуса от половинной
// разрядности здесь не страдает (порог сравнения — сотые доли).
type Vector []float32

// Index — векторы записей и то, чем они сняты.
//
// Имя модели и размерность хранятся ВМЕСТЕ с векторами намеренно: индекс,
// снятый другой моделью, состоит из таких же чисел и молча даёт правдоподобный
// шум. Это тот же класс, что «объявленный размер расходится с содержимым».
type Index struct {
	Model   string
	Dims    int
	Vectors map[int]Vector
}

// Hit — запись каталога и насколько она близка запросу.
type Hit struct {
	ID    int
	Score float64
}

// Embedder превращает текст в вектор. Живёт интерфейсом в пакете-потребителе:
// движок не знает, кто именно считает — внешняя служба, файл или заглушка.
type Embedder interface {
	Embed(text string) (Vector, error)
}

// ErrNoSemanticLayer сообщает, что смыслового слоя нет вовсе.
//
// Отдельная ошибка, а не пустой ответ: «ничего не нашлось» и «искать было
// нечем» — разные ответы, и человек, получивший пустоту вместо второго,
// заключит, что в базе такого нет.
var ErrNoSemanticLayer = errors.New("search: смыслового слоя нет — нужны индекс и эмбеддер")

// Cosine — близость двух векторов, от -1 до 1.
//
// Возвращает NaN, когда сравнивать нечего: разная размерность или вектор
// нулевой длины. NaN здесь не «ошибка вычисления», а честное «не знаю»: ноль
// поставил бы такую пару в один ряд с честно ортогональными.
func Cosine(a, b Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return math.NaN()
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return math.NaN()
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Nearest returns the entries closest to the query vector, best first.
//
// Порог обязателен и передаётся вызывающим: без него «ближайшие» находятся
// всегда, и запрос про то, чего в базе нет, вернёт правдоподобный список —
// ровно то, чего требует не допускать приёмка задачи.
func (ix Index) Nearest(q Vector, limit int, threshold float64) []Hit {
	if len(q) == 0 || ix.Dims != 0 && len(q) != ix.Dims {
		return nil // не сравниваем вектор с индексом, снятым иначе
	}
	hits := make([]Hit, 0, limit)
	for id, v := range ix.Vectors {
		s := Cosine(q, v)
		if math.IsNaN(s) || s < threshold {
			continue
		}
		hits = append(hits, Hit{ID: id, Score: s})
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		return a.ID - b.ID // одинаковая близость — устойчивый порядок по id
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// Semantic ищет по близости смысла.
type Semantic struct {
	index Index
	embed Embedder
}

// NewSemantic wires the index to whatever computes the query vector.
func NewSemantic(ix Index, e Embedder) Semantic { return Semantic{index: ix, embed: e} }

// Available reports whether the layer can answer at all.
func (s Semantic) Available() bool { return s.embed != nil && len(s.index.Vectors) > 0 }

// Search returns the entries closest in meaning to the query.
func (s Semantic) Search(query string, limit int, threshold float64) ([]Hit, error) {
	if !s.Available() {
		return nil, ErrNoSemanticLayer
	}
	v, err := s.embedQuery(query)
	if err != nil {
		return nil, err
	}
	return s.index.Nearest(v, limit, threshold), nil
}

// embed зовёт эмбеддер и сохраняет причину отказа как есть: подменять её своей
// значило бы стереть то, что человеку нужно чинить.
func (s Semantic) embedQuery(query string) (Vector, error) {
	v, err := s.embed.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("search: вектор запроса не получен: %w", err)
	}
	return v, nil
}
