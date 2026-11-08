package domain

import "errors"

// Двухуровневая память (ADR-0008): горячий слой и холодный.

// ErrInvalidNote — запись холодной памяти нарушает инвариант.
var ErrInvalidNote = errors.New("invalid memory note")

// ErrMemoryExists — память в этой папке уже заведена, перезаписывать нечем.
var ErrMemoryExists = errors.New("memory already exists")

// ErrNoteExists — запись с таким именем в холодной памяти уже есть.
var ErrNoteExists = errors.New("memory note already exists")

// DefaultHotLimit — потолок горячего слоя в знаках.
const DefaultHotLimit = 4000

// MaxHookRunes — потолок крючка: одна строка индекса.
const MaxHookRunes = 150

// ProblemKind — класс нарушения раскладки памяти.
type ProblemKind string

const (
	ProblemHotTooBig     ProblemKind = "hot-too-big"
	ProblemNoDescription ProblemKind = "no-description"
	ProblemNotIndexed    ProblemKind = "not-indexed"
	ProblemDangling      ProblemKind = "dangling-index"
	ProblemLongHook      ProblemKind = "long-hook"
)

// MemoryProblem — одно нарушение: что и где.
type MemoryProblem struct {
	Kind   ProblemKind
	File   string
	Detail string
}

// ColdNote — запись холодного слоя, как ее нашли на диске.
type ColdNote struct {
	File        string
	Name        string
	Description string
}

// IndexEntry — строка индекса холодной памяти.
type IndexEntry struct {
	File string
	Hook string
}

// NewColdNote validates a new cold note.
func NewColdNote(name, description string) (ColdNote, error) {
	return ColdNote{}, nil
}

// CheckMemory returns every violation of the layout, in a stable order.
func CheckMemory(hot string, hotLimit int, notes []ColdNote, index []IndexEntry) []MemoryProblem {
	return nil
}
