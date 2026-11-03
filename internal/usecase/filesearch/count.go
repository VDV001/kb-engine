package filesearch

import (
	"path/filepath"
	"strings"
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
	out := make(map[string]int, len(roots))
	for _, r := range roots {
		out[r] = 0
	}
	sep := string(filepath.Separator)
	for _, d := range ix.docs {
		p := d.Path()
		for _, r := range roots {
			if p == r || strings.HasPrefix(p, strings.TrimSuffix(r, sep)+sep) {
				out[r]++
			}
		}
	}
	return out
}
