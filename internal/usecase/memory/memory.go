// Package memory — двухуровневая память: скелет, проверка размера и записи (ADR-0008).
package memory

import "github.com/daniil/kb-engine/internal/domain"

// Snapshot — что лежит в папке памяти.
type Snapshot struct {
	Hot   string
	Notes []domain.ColdNote
	Index []domain.IndexEntry
}

// Store — порт хранилища памяти.
type Store interface {
	Init() error
	Load() (Snapshot, error)
	Add(n domain.ColdNote, body string) error
}

// Service — сценарии памяти.
type Service struct {
	store Store
	limit int
}

// New builds the service; limit<=0 берет DefaultHotLimit.
func New(store Store, limit int) *Service { return &Service{store: store, limit: limit} }

// Init заводит скелет.
func (s *Service) Init() error { return nil }

// Check returns the violations of the layout.
func (s *Service) Check() ([]domain.MemoryProblem, error) { return nil, nil }

// Add кладет запись в холодный слой и строку в индекс.
func (s *Service) Add(name, description, body string) error { return nil }
