package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

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

// Классы нарушений: их имена печатает `kbengine memory check` и читают скрипты.
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

var noteName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// NewColdNote validates a new cold note: имя — kebab-латиница (оно же имя файла),
// описание — одна строка до MaxHookRunes, потому что оно же крючок в индексе.
func NewColdNote(name, description string) (ColdNote, error) {
	description = strings.TrimSpace(description)
	switch {
	case !noteName.MatchString(name):
		return ColdNote{}, fmt.Errorf("%w: имя %q — латиница в нижнем регистре, цифры и дефисы", ErrInvalidNote, name)
	case description == "":
		return ColdNote{}, fmt.Errorf("%w: описание пустое", ErrInvalidNote)
	case strings.ContainsAny(description, "\r\n"):
		return ColdNote{}, fmt.Errorf("%w: описание в одну строку", ErrInvalidNote)
	case utf8.RuneCountInString(description) > MaxHookRunes:
		return ColdNote{}, fmt.Errorf("%w: описание длиннее %d знаков", ErrInvalidNote, MaxHookRunes)
	}
	return ColdNote{File: name + ".md", Name: name, Description: description}, nil
}

// CheckMemory returns every violation of the layout, in a stable order:
// горячий слой, записи холодного, затем индекс.
func CheckMemory(hot string, hotLimit int, notes []ColdNote, index []IndexEntry) []MemoryProblem {
	var ps []MemoryProblem
	if n := utf8.RuneCountInString(hot); n > hotLimit {
		ps = append(ps, MemoryProblem{ProblemHotTooBig, "HOT.md", fmt.Sprintf("%d знаков при лимите %d", n, hotLimit)})
	}
	indexed := make(map[string]bool, len(index))
	for _, e := range index {
		indexed[e.File] = true
	}
	exists := make(map[string]bool, len(notes))
	for _, n := range notes {
		exists[n.File] = true
		if strings.TrimSpace(n.Description) == "" {
			ps = append(ps, MemoryProblem{ProblemNoDescription, n.File, "в шапке нет description"})
		}
		if !indexed[n.File] {
			ps = append(ps, MemoryProblem{ProblemNotIndexed, n.File, "нет строки в cold/INDEX.md"})
		}
	}
	for _, e := range index {
		if !exists[e.File] {
			ps = append(ps, MemoryProblem{ProblemDangling, e.File, "индекс ведет на файл, которого нет"})
		}
		if n := utf8.RuneCountInString(e.Hook); n > MaxHookRunes {
			ps = append(ps, MemoryProblem{ProblemLongHook, e.File, fmt.Sprintf("крючок %d знаков при потолке %d", n, MaxHookRunes)})
		}
	}
	return ps
}
