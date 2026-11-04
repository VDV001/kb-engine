package filesearch

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// CountByRoot returns how many indexed files lie under each root, or nil
// before the first build. Граница — разделитель пути: «/w/ab» не входит в «/w/a».
//
// ponytail: проход по всем документам на каждый вызов, O(файлы × папки).
// Потолок: экран источников зовут по открытию панели, 40 тыс. файлов на пяти
// папках — доли миллисекунды. Путь: считать в сборке и хранить в Stats.
func (s *Service) CountByRoot(roots []string) map[string]int {
	s.mu.RLock()
	ix := s.ix
	s.mu.RUnlock()
	if ix == nil {
		return nil
	}
	return countUnder(ix.docs, roots)
}

// CountMatches returns how many files under each root match query: «M» в
// строке источников над выдачей. Выдача файлов срезана до limit, счет — нет.
//
// ponytail: поиск идет без среза и сортирует все совпадения, хотя нужен
// только счет; и выдача, и счет на одну букву запроса считают его дважды.
// Потолок: замер не делался — индекс в памяти, совпадений на короткий запрос
// тысячи. Путь: Index.Search, отдающий срез и счет по папкам за один проход.
func (s *Service) CountMatches(query string, roots []string) (map[string]int, error) {
	s.mu.RLock()
	ix, berr := s.ix, s.buildErr
	s.mu.RUnlock()
	if ix == nil {
		if berr != nil {
			return nil, fmt.Errorf("%w: %w", ErrBuildFailed, berr)
		}
		return nil, ErrNotBuilt
	}
	hits := ix.Search(query, len(ix.docs))
	docs := make([]domain.FileDoc, len(hits))
	for i, h := range hits {
		docs[i] = h.Doc
	}
	return countUnder(docs, roots), nil
}

// countUnder counts docs under each root, by the separator boundary.
func countUnder(docs []domain.FileDoc, roots []string) map[string]int {
	out := make(map[string]int, len(roots))
	for _, r := range roots {
		out[r] = 0
	}
	sep := string(filepath.Separator)
	prefixes := make([]string, len(roots))
	for i, r := range roots {
		prefixes[i] = strings.TrimSuffix(r, sep) + sep
	}
	for _, d := range docs {
		p := d.Path()
		for i, r := range roots {
			if p == r || strings.HasPrefix(p, prefixes[i]) {
				out[r]++
			}
		}
	}
	return out
}
