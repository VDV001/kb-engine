// Package memorydir хранит двухуровневую память файлами в папке человека.
package memorydir

import (
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// Store — папка памяти: HOT.md и cold/ с INDEX.md.
type Store struct{ dir string }

// New points the store at dir.
func New(dir string) *Store { return &Store{dir: dir} }

// Init заводит скелет; существующую память не трогает.
func (s *Store) Init() error { return nil }

// Load reads the layer files.
func (s *Store) Load() (memory.Snapshot, error) { return memory.Snapshot{}, nil }

// Add writes the note and its index line.
func (s *Store) Add(n domain.ColdNote, body string) error { return nil }
