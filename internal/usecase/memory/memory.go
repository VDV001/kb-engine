// Package memory — двухуровневая память: скелет, проверка размера и записи (ADR-0008).
package memory

import "github.com/daniil/kb-engine/internal/domain"

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
}

// Service — сценарии памяти.
type Service struct {
	store  Store
	limits Limits
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
	return &Service{store: store, limits: l}
}

// Init заводит скелет.
func (s *Service) Init() error { return s.store.Init() }

// Check returns the violations of the layout.
func (s *Service) Check() ([]domain.MemoryProblem, error) {
	snap, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	return domain.CheckMemory(domain.MemoryState{
		Hot: snap.Hot, HotLimit: s.limits.Hot,
		IndexRunes: snap.IndexRunes, IndexLimit: s.limits.Index,
		Notes: snap.Notes, Index: snap.Index,
	}), nil
}

// Add кладет запись в холодный слой и строку в индекс.
func (s *Service) Add(name, topic, description, body string) error {
	n, err := domain.NewColdNote(name, topic, description)
	if err != nil {
		return err
	}
	return s.store.Add(n, body)
}
