// Package memory — двухуровневая память: скелет, проверка размера и записи (ADR-0008).
package memory

import (
	"errors"

	"github.com/daniil/kb-engine/internal/domain"
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
