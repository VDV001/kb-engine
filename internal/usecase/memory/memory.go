// Package memory — двухуровневая память: скелет, проверка размера и записи (ADR-0008).
package memory

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Snapshot — что лежит в папке памяти.
type Snapshot struct {
	Hot        string
	IndexRunes int
	Notes      []domain.ColdNote
	Index      []domain.IndexEntry
}

// Store — порт хранилища памяти.
type Store interface {
	Init() error
	Load() (Snapshot, error)
	Add(n domain.ColdNote, body string) error
	SaveHot(hot string) error
	AddPending(n domain.ColdNote, body string) error
	Pending() ([]PendingNote, error)
	ReadPending(name string) (PendingNote, error)
	// PromotePending переносит запись из очереди в холодный слой со строкой индекса.
	PromotePending(name string) error
	// ArchivePending убирает запись из очереди в журнал под именем as: не удаляет.
	ArchivePending(name, as string) error
	// ColdTexts — записи холодного слоя с телом; журнал и очередь сюда не входят.
	ColdTexts() ([]NoteText, error)
	// LoadVectors возвращает ErrNoVectors, если индекс векторов еще не снят.
	LoadVectors() (VectorIndex, error)
	SaveVectors(v VectorIndex) error
}

// PendingNote — запись агента, ждущая решения человека.
type PendingNote struct {
	Note domain.ColdNote
	Body string
}

// Service — сценарии памяти.
type Service struct {
	store    Store
	limits   Limits
	now      func() time.Time
	embed    search.Embedder
	model    string
	portrait PortraitSource
}

// PortraitSource отдает текст одобренных фактов портрета DVK для блока «Кто я».
type PortraitSource interface {
	Text() (string, error)
}

// ErrNoPortrait — источник портрета не подключен: папка не выбрана.
var ErrNoPortrait = errors.New("memory: портрет не подключен")

// WithPortrait подключает портрет DVK как единственный источник блока «Кто я».
func (s *Service) WithPortrait(p PortraitSource) *Service {
	s.portrait = p
	return s
}

// Who переписывает блок «Кто я» в HOT.md из портрета. changed=false — блок уже
// актуален; ErrNoPortraitMarkers — HOT.md правили руками, его не переписывают.
func (s *Service) Who() (changed bool, err error) {
	if s.portrait == nil {
		return false, ErrNoPortrait
	}
	text, err := s.portrait.Text()
	if err != nil {
		return false, err
	}
	snap, err := s.store.Load()
	if err != nil {
		return false, err
	}
	hot, err := domain.ReplacePortrait(snap.Hot, text)
	if err != nil || hot == snap.Hot {
		return false, err
	}
	return true, s.store.SaveHot(hot)
}

// WithEmbedder подключает смысловой слой: службу эмбеддингов и имя ее модели.
func (s *Service) WithEmbedder(e search.Embedder, model string) *Service {
	s.embed, s.model = e, model
	return s
}

// WithClock replaces the clock used to name archived notes; для тестов.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Limits — потолки проверки; ноль берет значение по умолчанию.
type Limits struct {
	Hot   int
	Index int
}

// New builds the service.
func New(store Store, l Limits) *Service {
	if l.Hot <= 0 {
		l.Hot = domain.DefaultHotLimit
	}
	if l.Index <= 0 {
		l.Index = domain.DefaultIndexLimit
	}
	return &Service{store: store, limits: l, now: time.Now}
}

// Init заводит скелет.
func (s *Service) Init() error { return s.store.Init() }

// Check returns the violations of the layout.
func (s *Service) Check() ([]domain.MemoryProblem, error) {
	snap, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	st := domain.MemoryState{
		Hot: snap.Hot, HotLimit: s.limits.Hot,
		IndexRunes: snap.IndexRunes, IndexLimit: s.limits.Index,
		Notes: snap.Notes, Index: snap.Index,
	}
	switch vi, err := s.store.LoadVectors(); {
	case errors.Is(err, ErrNoVectors):
	case err != nil:
		return nil, err
	default:
		st.HasVectors = true
		st.VectorDigests = make(map[string]string, len(vi.Entries))
		for name, e := range vi.Entries {
			st.VectorDigests[name] = e.Digest
		}
	}
	return domain.CheckMemory(st), nil
}

// Topics собирает перечень тем горячего слоя из тем записей. changed=false —
// перечень уже актуален и файл не тронут; ErrNoTopicMarkers — HOT.md правили
// руками, и его не переписывают.
func (s *Service) Topics() (changed bool, err error) {
	snap, err := s.store.Load()
	if err != nil {
		return false, err
	}
	hot, err := domain.ReplaceTopics(snap.Hot, snap.Notes)
	if err != nil || hot == snap.Hot {
		return false, err
	}
	return true, s.store.SaveHot(hot)
}

// Add кладет запись в холодный слой и строку в индекс, затем обновляет перечень
// тем. refreshed=false при nil-ошибке — в HOT.md нет меток перечня: запись
// сохранена, а перечень человеку придется вести самому.
func (s *Service) Add(name, topic, description, body string) (refreshed bool, err error) {
	n, err := domain.NewColdNote(name, topic, description)
	if err != nil {
		return false, err
	}
	if err := s.store.Add(n, body); err != nil {
		return false, err
	}
	if _, err := s.Topics(); err != nil {
		if errors.Is(err, domain.ErrNoTopicMarkers) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Propose кладет запись агента в очередь одобрения: ни холодный, ни горячий слой,
// ни перечень тем не меняются, пока человек не решит.
func (s *Service) Propose(name, topic, description, body string) error {
	n, err := domain.NewColdNote(name, topic, description)
	if err != nil {
		return err
	}
	n.Origin = domain.OriginAgent
	return s.store.AddPending(n, body)
}

// Pending lists the queue.
func (s *Service) Pending() ([]PendingNote, error) { return s.store.Pending() }

// Approve moves a queued note to the layer the human chose. В горячий слой уходит
// одна строка (description) как правило, а сама запись — в журнал; потолок слоя
// не обходится: при нехватке места запись остается в очереди.
func (s *Service) Approve(name string, to domain.Layer) error {
	p, err := s.store.ReadPending(name)
	if err != nil {
		return err
	}
	if to == domain.LayerCold {
		if err := s.store.PromotePending(name); err != nil {
			return err
		}
		if _, err := s.Topics(); err != nil && !errors.Is(err, domain.ErrNoTopicMarkers) {
			return err
		}
		return nil
	}
	snap, err := s.store.Load()
	if err != nil {
		return err
	}
	hot, err := domain.AppendRule(snap.Hot, p.Note.Description)
	if err != nil {
		return err
	}
	if n := utf8.RuneCountInString(hot); n > s.limits.Hot {
		return fmt.Errorf("%w: стало бы %d знаков при потолке %d", domain.ErrHotFull, n, s.limits.Hot)
	}
	// ponytail: два шага без транзакции. Сбой после SaveHot оставит правило и в
	// горячем слое, и в очереди; повторное одобрение продублирует строку. Путь
	// вверх: проверять наличие строки перед дописыванием.
	if err := s.store.SaveHot(hot); err != nil {
		return err
	}
	return s.store.ArchivePending(name, s.archiveName("approved", name))
}

// Reject archives a queued note to the journal instead of deleting it.
func (s *Service) Reject(name string) error {
	if _, err := s.store.ReadPending(name); err != nil {
		return err
	}
	return s.store.ArchivePending(name, s.archiveName("rejected", name))
}

func (s *Service) archiveName(verdict, name string) string {
	return fmt.Sprintf("%s-%s-%s.md", s.now().Format("2006-01-02"), verdict, name)
}
