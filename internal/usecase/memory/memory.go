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
	// JournalNames — имена файлов journal/ по возрастанию.
	JournalNames() ([]string, error)
	// AppendDecision дописывает решение человека в журнал решений (только дописывается).
	AppendDecision(line string) error
	// Decisions — последние n записей журнала решений.
	Decisions(n int) ([]string, error)
	// Seal запоминает отпечаток текущего HOT.md как «принятый»; SealedDigest его
	// отдает ("" — печати нет).
	Seal() error
	SealedDigest() (string, error)
}

// PendingNote — запись агента, ждущая решения человека.
type PendingNote struct {
	Note domain.ColdNote
	Body string
	// Since — когда предложение легло в очередь (время файла); нулевое — неизвестно.
	Since time.Time
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
	// Потолок и здесь: портрет, который распухает, вытесняет правила. Уменьшать
	// переполненный слой можно всегда.
	if n, old := utf8.RuneCountInString(hot), utf8.RuneCountInString(snap.Hot); n > s.limits.Hot && n > old {
		return false, fmt.Errorf("%w: с портретом стало бы %d знаков при потолке %d", domain.ErrHotFull, n, s.limits.Hot)
	}
	if err := s.store.SaveHot(hot); err != nil {
		return false, err
	}
	return true, s.decide("%s", domain.DecisionTextPortrait)
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
	return s.checkSnapshot(snap)
}

// checkSnapshot checks an already loaded snapshot: окну не нужно читать папку дважды.
func (s *Service) checkSnapshot(snap Snapshot) ([]domain.MemoryProblem, error) {
	st, err := s.state(snap)
	if err != nil {
		return nil, err
	}
	return domain.CheckMemory(st), nil
}

// state gathers what the layout check and the health both read.
func (s *Service) state(snap Snapshot) (domain.MemoryState, error) {
	sealed, err := s.store.SealedDigest()
	if err != nil {
		return domain.MemoryState{}, err
	}
	st := domain.MemoryState{
		Hot: snap.Hot, HotLimit: s.limits.Hot,
		IndexRunes: snap.IndexRunes, IndexLimit: s.limits.Index,
		Notes: snap.Notes, Index: snap.Index,
		HotDigest: domain.HotDigest(snap.Hot), SealDigest: sealed,
	}
	switch vi, err := s.store.LoadVectors(); {
	case errors.Is(err, ErrNoVectors):
	case err != nil:
		return domain.MemoryState{}, err
	default:
		st.HasVectors = true
		st.VectorDigests = make(map[string]string, len(vi.Entries))
		for name, e := range vi.Entries {
			st.VectorDigests[name] = e.Digest
		}
	}
	return st, nil
}

// decide writes one decision of the person to the journal; одна строка, с датой.
// Решение, которое не удалось записать, возвращается ошибкой: действие уже сделано,
// и человеку надо знать, что следа в журнале нет.
func (s *Service) decide(format string, args ...any) error {
	line := s.now().Format("2006-01-02 15:04") + " " + fmt.Sprintf(format, args...)
	if err := s.store.AppendDecision(line); err != nil {
		return fmt.Errorf("действие выполнено, но журнал решений не записан: %w", err)
	}
	return nil
}

// Topics собирает перечень тем горячего слоя из тем записей. changed=false —
// перечень уже актуален и файл не тронут; ErrNoTopicMarkers — HOT.md правили
// руками, и его не переписывают.
func (s *Service) Topics() (changed bool, err error) {
	changed, err = s.topics()
	if err == nil && changed {
		err = s.decide("%s", domain.DecisionTextTopics)
	}
	return changed, err
}

func (s *Service) topics() (changed bool, err error) {
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
	if _, err := s.topics(); err != nil {
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

// Approve moves a queued note to the layer the person chose. В горячий слой уходит
// одна строка (description) как правило, а сама запись — в журнал; потолок слоя
// не обходится: при нехватке места запись остается в очереди. Запись из очереди
// проверяется заново: файл могли положить мимо Propose. Решение остается в журнале.
func (s *Service) Approve(name string, to domain.Layer) error {
	p, err := s.store.ReadPending(name)
	if err != nil {
		return err
	}
	if err := domain.ValidateNote(p.Note); err != nil {
		return err
	}
	if to == domain.LayerCold {
		return s.approveCold(name, p)
	}
	return s.approveHot(name, p)
}

func (s *Service) approveCold(name string, p PendingNote) error {
	if err := s.store.PromotePending(name); err != nil {
		return err
	}
	if _, err := s.topics(); err != nil && !errors.Is(err, domain.ErrNoTopicMarkers) {
		return err
	}
	return s.decide(domain.DecisionPrefixApproveCold+"%s (тема «%s»)", name, p.Note.Topic)
}

func (s *Service) approveHot(name string, p PendingNote) error {
	snap, err := s.store.Load()
	if err != nil {
		return err
	}
	hot, err := domain.AppendRule(snap.Hot, p.Note.Description)
	if err != nil {
		return err
	}
	verdict := domain.DecisionPrefixApproveHot + "%s — «%s»"
	if hot == snap.Hot {
		verdict += domain.DecisionSuffixDup
	} else {
		if n := utf8.RuneCountInString(hot); n > s.limits.Hot {
			return fmt.Errorf("%w: стало бы %d знаков при потолке %d", domain.ErrHotFull, n, s.limits.Hot)
		}
		// ponytail: два шага без транзакции. Сбой после SaveHot оставит правило и в
		// горячем слое, и в очереди; повторное одобрение правило не продублирует
		// (AppendRule узнает его), а запись уйдет в журнал.
		if err := s.store.SaveHot(hot); err != nil {
			return err
		}
	}
	if err := s.store.ArchivePending(name, s.archiveName("approved", name)); err != nil {
		return err
	}
	return s.decide(verdict, name, p.Note.Description)
}

// Reject archives a queued note to the journal instead of deleting it.
func (s *Service) Reject(name string) error {
	p, err := s.store.ReadPending(name)
	if err != nil {
		return err
	}
	if err := s.store.ArchivePending(name, s.archiveName("rejected", name)); err != nil {
		return err
	}
	return s.decide(domain.DecisionPrefixReject+"%s — «%s»", name, p.Note.Description)
}

// Seal accepts the current HOT.md as is: человек прочитал правку, сделанную мимо
// системы, и берет ее на себя. Подтверждение остается в журнале.
func (s *Service) Seal() error {
	if err := s.store.Seal(); err != nil {
		return err
	}
	return s.decide("%s", domain.DecisionTextSeal)
}

func (s *Service) archiveName(verdict, name string) string {
	return fmt.Sprintf("%s-%s-%s.md", s.now().Format("2006-01-02"), verdict, name)
}

// Overview — все, что показывает экран памяти: слои, очередь, журнал, решения,
// нарушения и действующие потолки.
type Overview struct {
	Snapshot  Snapshot
	Pending   []PendingNote
	Journal   []string
	Decisions []string
	Problems  []domain.MemoryProblem
	Limits    Limits
	// Health и Events — здоровье памяти и все решения журнала, разобранные (для ритма).
	Health domain.Health
	Events []domain.Decision
	// Today — день, от которого считались возрасты (по часам сценария).
	Today time.Time
}

// recentDecisions — сколько последних решений показывает экран.
const recentDecisions = 20

// Overview gathers the state for the window in one pass: папка читается один раз.
func (s *Service) Overview() (Overview, error) {
	snap, err := s.store.Load()
	if err != nil {
		return Overview{}, err
	}
	pending, err := s.store.Pending()
	if err != nil {
		return Overview{}, err
	}
	journal, err := s.store.JournalNames()
	if err != nil {
		return Overview{}, err
	}
	lines, err := s.store.Decisions(allDecisions)
	if err != nil {
		return Overview{}, err
	}
	problems, err := s.checkSnapshot(snap)
	if err != nil {
		return Overview{}, err
	}
	health, events, err := s.assess(snap, pending, lines)
	if err != nil {
		return Overview{}, err
	}
	decisions := lines[max(0, len(lines)-recentDecisions):]
	return Overview{Snapshot: snap, Pending: pending, Journal: journal, Decisions: decisions, Problems: problems, Limits: s.limits, Health: health, Events: events, Today: domain.DayOf(s.now())}, nil
}
