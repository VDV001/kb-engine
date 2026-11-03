package filesearch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNotBuilt is returned when Search is called before Build.
var ErrNotBuilt = errors.New("file index is not built")

// ErrBuildFailed is returned by Search when the last build failed and there is
// no earlier index to answer from. Отличается от ErrNotBuilt намеренно: «еще
// собирается» при упавшей сборке держало окно на экране индексации вечно.
var ErrBuildFailed = errors.New("file index build failed")

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
	Walk(ctx context.Context, fn func(domain.FileDoc) error) error
	ReadText(path string) (string, error)
	// Check reports whether a path is one the walk would index, without reading it.
	Check(path string) error
}

// Stats reports what a build did.
type Stats struct {
	Indexed int
	Skipped int    // файлы, текст которых прочитать не удалось
	Reused  int    // файлы, взятые из прошлого индекса без чтения: не менялись
	Warning string // почему снимок не пригодился (не прочитан, отклонен); пусто — все штатно
}

// ErrSaveFailed is returned when the index was built and swapped in but could
// not be written to the store: поиск работает, следующий запуск соберет заново.
var ErrSaveFailed = errors.New("file index built but not saved")

// Store keeps the index between runs. Load reports false when there is nothing
// usable to load (no file, other version): that is a full build, not an error.
type Store interface {
	Load() (Snapshot, bool, error)
	Save(Snapshot) error
}

// Option configures a Service.
type Option func(*Service)

// WithStore keeps the index in st: the first build loads it and rereads only
// the files that changed, every build saves it.
func WithStore(st Store) Option { return func(s *Service) { s.store = st } }

// Service builds the file index and answers queries with snippets.
//
// Безопасен для одновременного поиска и пересборки: окно, MCP и HTTP ищут,
// пока фоновая пересборка готовит новый индекс. Новый индекс подменяет старый
// целиком под замком, поиск никогда не видит наполовину собранный.
type Service struct {
	src   Source
	store Store            // nil — индекс живет только в памяти
	now   func() time.Time // часы снаружи: тесты и сборка решают, что «свежий файл»

	mu       sync.RWMutex
	ix       *Index
	stats    Stats
	buildErr error // ошибка последней сборки, пока индекса нет
}

// NewService returns a service reading through src.
func NewService(src Source, opts ...Option) *Service {
	s := &Service{src: src, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Build walks the source and indexes every file whose text can be read.
//
// Нечитаемый файл не роняет сборку: из тысяч файлов один битый — норма. Но и
// не пропадает молча — он посчитан в Skipped, чтобы «нашлось меньше, чем
// лежит» было видно числом.
func (s *Service) Build(ctx context.Context) (Stats, error) {
	return s.Replace(ctx, s.source())
}

// Replace builds a fresh index from src and, only if that succeeds, swaps both
// the source and the index in. Так меняются папки в окне: до конца сборки
// поиск отвечает по старому индексу, а при ошибке старый остается.
//
// Сборка инкрементальная: файл с тем же путем, размером и временем изменения
// берется из прежнего индекса (в памяти или из Store) без чтения.
//
// ponytail: на время сборки в памяти два индекса — старый и новый. Потолок:
// ~/git целиком (замер 03.11: ~1 ГБ на 40 тыс. файлов). Путь: сегменты на диске
// с mmap вместо индекса целиком в памяти.
func (s *Service) Replace(ctx context.Context, src Source) (Stats, error) {
	prev, warning := s.previous()
	b := builder{prev: prev, byPath: prev.byPath(), ix: NewIndex(), oldToNew: map[int32]int32{}, seen: map[string]bool{}}
	b.ix.builtAt = s.now()
	b.st.Warning = warning
	err := src.Walk(ctx, func(d domain.FileDoc) error { return b.visit(src, d) })
	st := b.st
	ix := b.ix
	if err != nil {
		err = fmt.Errorf("walk files: %w", err)
		s.mu.Lock()
		if s.ix == nil {
			s.buildErr = err // отвечать нечем — причина дойдет до спрашивающего
		} // иначе прежний индекс продолжает отвечать, и ошибка «индекса нет» была бы неправдой
		s.mu.Unlock()
		return st, err
	}
	ix.carryOver(prev, b.oldToNew)
	s.mu.Lock()
	s.src, s.ix, s.stats, s.buildErr = src, ix, st, nil
	s.mu.Unlock()
	// Без изменений снимок не переписывается: это ~100 МБ на каждый запуск.
	unchanged := prev != nil && st.Reused == st.Indexed && len(prev.docs) == st.Indexed
	if s.store != nil && !unchanged {
		if err := s.store.Save(ix.Snapshot()); err != nil {
			return st, fmt.Errorf("%w: %w", ErrSaveFailed, err)
		}
	}
	return st, nil
}

// racyWindow — файлы, измененные позже, чем за столько до прошлой сборки,
// перечитываются: на ФС с грубым mtime (HFS+, FAT, SMB — до 2 с) файл мог быть
// сохранен второй раз с тем же размером и тем же временем (как racy-clean в git).
const racyWindow = 2 * time.Second

// builder holds one build's state: what is reused from prev and what is read.
type builder struct {
	prev     *Index
	byPath   map[string]int32
	ix       *Index
	oldToNew map[int32]int32
	seen     map[string]bool // пересекающиеся корни дают тот же путь дважды
	st       Stats
}

func (b *builder) visit(src Source, d domain.FileDoc) error {
	if b.seen[d.Path()] {
		return nil
	}
	b.seen[d.Path()] = true
	if old, ok := b.byPath[d.Path()]; ok && b.reusable(b.prev.docs[old], d) {
		b.oldToNew[old] = b.ix.addDoc(d)
		b.st.Reused++
		b.st.Indexed++
		return nil
	}
	text, err := src.ReadText(d.Path())
	if err != nil {
		b.st.Skipped++
		return nil
	}
	b.ix.Add(d, text)
	b.st.Indexed++
	return nil
}

// reusable reports whether a walked file is the one recorded before: same size
// and modification time, and old enough not to be racy. Содержимое не
// сравнивается — для этого его пришлось бы прочитать, а экономия в том, чтобы
// не читать.
func (b *builder) reusable(old, now domain.FileDoc) bool {
	return old.Size() == now.Size() && old.ModTime().Equal(now.ModTime()) &&
		now.ModTime().Before(b.prev.builtAt.Add(-racyWindow))
}

// previous returns the index to reuse files from: the one in memory, or the
// stored one on the first build. Битый или чужой снимок — полная сборка, но
// не молча: причина уходит в Stats.Warning (ревью #374).
func (s *Service) previous() (*Index, string) {
	s.mu.RLock()
	ix := s.ix
	s.mu.RUnlock()
	if ix != nil || s.store == nil {
		return ix, ""
	}
	snap, ok, err := s.store.Load()
	if err != nil {
		return nil, "снимок индекса не прочитан, полная сборка: " + err.Error()
	}
	if !ok {
		return nil, ""
	}
	ix, err = IndexFromSnapshot(snap)
	if err != nil {
		return nil, "снимок индекса отклонен, полная сборка: " + err.Error()
	}
	return ix, ""
}

// Unload drops the index from memory and keeps the source: Read and Check
// answer as before, Search reports ErrNotBuilt until the next Build, which
// loads the index back from the store without rereading unchanged files.
func (s *Service) Unload() {
	s.mu.Lock()
	s.ix, s.stats, s.buildErr = nil, Stats{}, nil
	s.mu.Unlock()
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
	ix, berr, src := s.ix, s.buildErr, s.src
	s.mu.RUnlock()
	if ix == nil {
		if berr != nil {
			return nil, fmt.Errorf("%w: %w", ErrBuildFailed, berr)
		}
		return nil, ErrNotBuilt
	}
	hits := ix.Search(query, limit)
	for i := range hits {
		// Сниппет читается заново, а не хранится в индексе: текст всех файлов
		// в памяти весил бы как сами файлы, а читать нужно лишь показанные.
		if text, err := src.ReadText(hits[i].Doc.Path()); err == nil {
			hits[i].Snippet = Snippet(text, query, snippetRunes)
		}
	}
	return hits, nil
}

// Read returns one file's text through the same source, and so under the same
// rules: a path the walk would not index is refused with ErrNotAllowed.
func (s *Service) Read(path string) (string, error) { return s.source().ReadText(path) }

// Check reports whether a path may be opened, under the same rules as Read but
// without reading the file.
func (s *Service) Check(path string) error { return s.source().Check(path) }

func (s *Service) source() Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.src
}
