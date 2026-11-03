package filesearch

import (
	"errors"
	"fmt"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNotBuilt is returned when Search is called before Build.
var ErrNotBuilt = errors.New("file index is not built")

// snippetRunes — длина строки совпадения в выдаче: хватает на мысль, не
// хватает на абзац.
const snippetRunes = 160

// Source is what the service reads files through: a walk over the files to
// index and a way to get one file's text. The adapter decides which folders,
// which files are skipped and how text is extracted.
type Source interface {
	Walk(fn func(domain.FileDoc) error) error
	ReadText(path string) (string, error)
}

// Stats reports what a build did.
type Stats struct {
	Indexed int
	Skipped int // файлы, текст которых прочитать не удалось
}

// Service builds the file index and answers queries with snippets.
type Service struct {
	src Source
	ix  *Index
}

// NewService returns a service reading through src.
func NewService(src Source) *Service { return &Service{src: src} }

// Build walks the source and indexes every file whose text can be read.
//
// Нечитаемый файл не роняет сборку: из тысяч файлов один битый — норма. Но и
// не пропадает молча — он посчитан в Skipped, чтобы «нашлось меньше, чем
// лежит» было видно числом.
func (s *Service) Build() (Stats, error) {
	ix := NewIndex()
	var st Stats
	err := s.src.Walk(func(d domain.FileDoc) error {
		text, err := s.src.ReadText(d.Path())
		if err != nil {
			st.Skipped++
			return nil
		}
		ix.Add(d, text)
		st.Indexed++
		return nil
	})
	if err != nil {
		return st, fmt.Errorf("walk files: %w", err)
	}
	s.ix = ix
	return st, nil
}

// Search finds files by query and attaches the matching line of each.
func (s *Service) Search(query string, limit int) ([]Hit, error) {
	if s.ix == nil {
		return nil, ErrNotBuilt
	}
	hits := s.ix.Search(query, limit)
	for i := range hits {
		// Сниппет читается заново, а не хранится в индексе: текст всех файлов
		// в памяти весил бы как сами файлы, а читать нужно лишь показанные.
		if text, err := s.src.ReadText(hits[i].Doc.Path()); err == nil {
			hits[i].Snippet = Snippet(text, query, snippetRunes)
		}
	}
	return hits, nil
}
