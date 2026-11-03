package filesearch

import (
	"errors"
	"fmt"
	"sync"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNotBuilt is returned when Search is called before Build.
var ErrNotBuilt = errors.New("file index is not built")

// ErrNotAllowed is what a Source returns for a path it refuses to read:
// outside its roots, a secret, a junk folder. It belongs to the port, not to
// one adapter, so every surface (HTTP, MCP) maps it the same way.
var ErrNotAllowed = errors.New("path is not allowed")

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
//
// Безопасен для одновременного поиска и пересборки: окно, MCP и HTTP ищут,
// пока фоновая пересборка готовит новый индекс. Новый индекс подменяет старый
// целиком под замком, поиск никогда не видит наполовину собранный.
type Service struct {
	src Source

	mu    sync.RWMutex
	ix    *Index
	stats Stats
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
	s.mu.Lock()
	s.ix, s.stats = ix, st
	s.mu.Unlock()
	return st, nil
}

// Stats returns what the last successful build did.
func (s *Service) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// Search finds files by query and attaches the matching line of each.
func (s *Service) Search(query string, limit int) ([]Hit, error) {
	s.mu.RLock()
	ix := s.ix
	s.mu.RUnlock()
	if ix == nil {
		return nil, ErrNotBuilt
	}
	hits := ix.Search(query, limit)
	for i := range hits {
		// Сниппет читается заново, а не хранится в индексе: текст всех файлов
		// в памяти весил бы как сами файлы, а читать нужно лишь показанные.
		if text, err := s.src.ReadText(hits[i].Doc.Path()); err == nil {
			hits[i].Snippet = Snippet(text, query, snippetRunes)
		}
	}
	return hits, nil
}

// Read returns one file's text through the same source, and so under the same
// rules: a path the walk would not index is refused with ErrNotAllowed.
func (s *Service) Read(path string) (string, error) { return s.src.ReadText(path) }
