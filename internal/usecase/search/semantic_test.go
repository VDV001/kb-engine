package search_test

import (
	"errors"
	"math"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/search"
)

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a, b search.Vector
		want float64
	}{
		{"одинаковые", search.Vector{1, 0, 0}, search.Vector{1, 0, 0}, 1},
		{"перпендикулярные", search.Vector{1, 0}, search.Vector{0, 1}, 0},
		{"противоположные", search.Vector{1, 0}, search.Vector{-1, 0}, -1},
		{"длина не важна", search.Vector{2, 2}, search.Vector{8, 8}, 1},
		// Нулевой вектор не «ортогонален всему», а не имеет направления вовсе.
		// Вернуть для него 0 значит поставить его в один ряд с честно далёкими.
		{"нулевой вектор", search.Vector{0, 0}, search.Vector{1, 1}, math.NaN()},
		{"разная размерность", search.Vector{1, 0}, search.Vector{1, 0, 0}, math.NaN()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := search.Cosine(tt.a, tt.b)
			if math.IsNaN(tt.want) {
				if !math.IsNaN(got) {
					t.Errorf("Cosine = %v, ожидалось «не знаю» (NaN)", got)
				}
				return
			}
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("Cosine = %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

func TestIndex_Nearest(t *testing.T) {
	ix := search.Index{
		Model: "тестовая",
		Dims:  2,
		Vectors: map[int]search.Vector{
			10: {1, 0},      // ровно то, что спросили
			20: {0.9, 0.44}, // рядом
			30: {0, 1},      // ортогонально
			40: {-1, 0},     // противоположно
		},
	}
	got := ix.Nearest(search.Vector{1, 0}, 3, 0.5)
	if len(got) != 2 {
		t.Fatalf("нашлось %d, ожидалось 2 (порог 0.5 отсекает ортогональное и противоположное): %+v", len(got), got)
	}
	if got[0].ID != 10 || got[1].ID != 20 {
		t.Errorf("порядок %d,%d — ожидалось 10,20 (ближайшее первым)", got[0].ID, got[1].ID)
	}
	if got[0].Score < got[1].Score {
		t.Error("оценка обязана убывать")
	}
}

// Отрицательный контроль, которого требует приёмка задачи: запрос про то, чего
// в базе нет, не имеет права вернуть «похожее» с высокой уверенностью.
func TestIndex_Nearest_nothingCloseEnough(t *testing.T) {
	ix := search.Index{Model: "тестовая", Dims: 2, Vectors: map[int]search.Vector{
		10: {0, 1}, 20: {-1, 0},
	}}
	if got := ix.Nearest(search.Vector{1, 0}, 5, 0.5); len(got) != 0 {
		t.Errorf("вернулось %d записей, ожидался пустой ответ: %+v", len(got), got)
	}
}

// Индекс, снятый другой моделью, сравнивать нельзя: числа те же, смысл другой.
// Молча сравнить их значило бы выдать шум за близость.
func TestIndex_Nearest_dimensionMismatch(t *testing.T) {
	ix := search.Index{Model: "тестовая", Dims: 3, Vectors: map[int]search.Vector{10: {1, 0, 0}}}
	if got := ix.Nearest(search.Vector{1, 0}, 5, 0.1); len(got) != 0 {
		t.Errorf("размерность запроса не совпала с индексом, а ответ непустой: %+v", got)
	}
}

// Semantic — три ответа, а не два: нашлось · не нашлось · слоя нет.
func TestSemantic_threeAnswers(t *testing.T) {
	ix := search.Index{Model: "тестовая", Dims: 2, Vectors: map[int]search.Vector{10: {1, 0}}}

	t.Run("слоя нет: эмбеддера не передали", func(t *testing.T) {
		s := search.NewSemantic(ix, nil)
		_, err := s.Search("что угодно", 5, 0.5)
		if !errors.Is(err, search.ErrNoSemanticLayer) {
			t.Fatalf("ошибка %v, ожидалась ErrNoSemanticLayer", err)
		}
	})

	t.Run("слоя нет: индекс пуст", func(t *testing.T) {
		s := search.NewSemantic(search.Index{}, stubEmbedder{v: search.Vector{1, 0}})
		_, err := s.Search("что угодно", 5, 0.5)
		if !errors.Is(err, search.ErrNoSemanticLayer) {
			t.Fatalf("ошибка %v, ожидалась ErrNoSemanticLayer", err)
		}
	})

	t.Run("нашлось", func(t *testing.T) {
		s := search.NewSemantic(ix, stubEmbedder{v: search.Vector{1, 0}})
		hits, err := s.Search("что угодно", 5, 0.5)
		if err != nil || len(hits) != 1 {
			t.Fatalf("hits=%d err=%v, ожидалась одна запись без ошибки", len(hits), err)
		}
	})

	t.Run("не нашлось — это не ошибка", func(t *testing.T) {
		s := search.NewSemantic(ix, stubEmbedder{v: search.Vector{0, 1}})
		hits, err := s.Search("что угодно", 5, 0.5)
		if err != nil {
			t.Fatalf("пустой ответ не ошибка, получено %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("hits=%d, ожидался ноль", len(hits))
		}
	})

	t.Run("эмбеддер отказал — говорим об этом, а не отдаём пустоту", func(t *testing.T) {
		boom := errors.New("модель недоступна")
		s := search.NewSemantic(ix, stubEmbedder{err: boom})
		if _, err := s.Search("что угодно", 5, 0.5); !errors.Is(err, boom) {
			t.Fatalf("ошибка %v, ожидалась %v", err, boom)
		}
	})
}

type stubEmbedder struct {
	v   search.Vector
	err error
}

func (s stubEmbedder) Embed(text string) (search.Vector, error) { return s.v, s.err }
