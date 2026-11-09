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

// DefaultIndexLimit — потолок cold/INDEX.md в знаках. Индекс в сессию не грузится,
// это обзор для человека: разросшийся индекс перестает быть обзором.
const DefaultIndexLimit = 30000

// MaxHookRunes — потолок крючка: одна строка индекса.
const MaxHookRunes = 150

// MaxTopicRunes — потолок названия темы: она попадает в перечень тем горячего слоя.
const MaxTopicRunes = 40

// Метки блока «перечень тем» в HOT.md: между ними `kbengine memory topics` держит
// список тем холодного слоя (ADR-0008: без перечня правила без слов-триггеров теряются).
const (
	TopicsStart = "<!-- topics:start -->"
	TopicsEnd   = "<!-- topics:end -->"
)

// ProblemKind — класс нарушения раскладки памяти.
type ProblemKind string

// Классы нарушений: их имена печатает `kbengine memory check` и читают скрипты.
const (
	ProblemHotTooBig     ProblemKind = "hot-too-big"
	ProblemNoDescription ProblemKind = "no-description"
	ProblemNotIndexed    ProblemKind = "not-indexed"
	ProblemDangling      ProblemKind = "dangling-index"
	ProblemLongHook      ProblemKind = "long-hook"
	ProblemNoTopic       ProblemKind = "no-topic"
	ProblemNoTopicList   ProblemKind = "no-topic-list"
	ProblemIndexTooBig   ProblemKind = "index-too-big"
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
	Topic       string
	Description string
}

// MemoryState — всё, что видит проверка раскладки. Лимиты подставляет сценарий;
// нулевой лимит не отключает проверку, а объявляет любой непустой слой нарушением.
type MemoryState struct {
	Hot        string
	HotLimit   int
	IndexRunes int
	IndexLimit int
	Notes      []ColdNote
	Index      []IndexEntry
}

// IndexEntry — строка индекса холодной памяти.
type IndexEntry struct {
	File string
	Hook string
}

var noteName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// NewColdNote validates a new cold note: имя — kebab-латиница (оно же имя файла),
// тема — одна строка до MaxTopicRunes (по ней собирается перечень тем горячего слоя),
// описание — одна строка до MaxHookRunes, потому что оно же крючок в индексе.
func NewColdNote(name, topic, description string) (ColdNote, error) {
	topic = strings.TrimSpace(topic)
	description = strings.TrimSpace(description)
	switch {
	case !noteName.MatchString(name):
		return ColdNote{}, fmt.Errorf("%w: имя %q — латиница в нижнем регистре, цифры и дефисы", ErrInvalidNote, name)
	case topic == "":
		return ColdNote{}, fmt.Errorf("%w: тема пустая", ErrInvalidNote)
	case strings.ContainsAny(topic, "\r\n"):
		return ColdNote{}, fmt.Errorf("%w: тема в одну строку", ErrInvalidNote)
	case utf8.RuneCountInString(topic) > MaxTopicRunes:
		return ColdNote{}, fmt.Errorf("%w: тема длиннее %d знаков", ErrInvalidNote, MaxTopicRunes)
	case description == "":
		return ColdNote{}, fmt.Errorf("%w: описание пустое", ErrInvalidNote)
	case strings.ContainsAny(description, "\r\n"):
		return ColdNote{}, fmt.Errorf("%w: описание в одну строку", ErrInvalidNote)
	case utf8.RuneCountInString(description) > MaxHookRunes:
		return ColdNote{}, fmt.Errorf("%w: описание длиннее %d знаков", ErrInvalidNote, MaxHookRunes)
	}
	return ColdNote{File: name + ".md", Name: name, Topic: topic, Description: description}, nil
}

// HasTopicList says whether the hot layer carries a non-empty topic list block.
func HasTopicList(hot string) bool {
	_, rest, ok := strings.Cut(hot, TopicsStart)
	if !ok {
		return false
	}
	body, _, ok := strings.Cut(rest, TopicsEnd)
	return ok && strings.TrimSpace(body) != ""
}

// CheckMemory returns every violation of the layout, in a stable order:
// горячий слой, записи холодного, затем индекс.
func CheckMemory(st MemoryState) []MemoryProblem {
	var ps []MemoryProblem
	if n := utf8.RuneCountInString(st.Hot); n > st.HotLimit {
		ps = append(ps, MemoryProblem{ProblemHotTooBig, "HOT.md", fmt.Sprintf("%d знаков при лимите %d", n, st.HotLimit)})
	}
	if len(st.Notes) > 0 && !HasTopicList(st.Hot) {
		ps = append(ps, MemoryProblem{ProblemNoTopicList, "HOT.md", "записи есть, перечня тем между " + TopicsStart + " и " + TopicsEnd + " нет: kbengine memory topics"})
	}
	indexed := make(map[string]bool, len(st.Index))
	for _, e := range st.Index {
		indexed[e.File] = true
	}
	exists := make(map[string]bool, len(st.Notes))
	for _, n := range st.Notes {
		exists[n.File] = true
		if strings.TrimSpace(n.Description) == "" {
			ps = append(ps, MemoryProblem{ProblemNoDescription, n.File, "в шапке нет description"})
		}
		if strings.TrimSpace(n.Topic) == "" {
			ps = append(ps, MemoryProblem{ProblemNoTopic, n.File, "в шапке нет topic"})
		}
		if !indexed[n.File] {
			ps = append(ps, MemoryProblem{ProblemNotIndexed, n.File, "нет строки в cold/INDEX.md"})
		}
	}
	for _, e := range st.Index {
		if !exists[e.File] {
			ps = append(ps, MemoryProblem{ProblemDangling, e.File, "индекс ведет на файл, которого нет"})
		}
		if n := utf8.RuneCountInString(e.Hook); n > MaxHookRunes {
			ps = append(ps, MemoryProblem{ProblemLongHook, e.File, fmt.Sprintf("крючок %d знаков при потолке %d", n, MaxHookRunes)})
		}
	}
	if st.IndexRunes > st.IndexLimit {
		ps = append(ps, MemoryProblem{ProblemIndexTooBig, "cold/INDEX.md", fmt.Sprintf("%d знаков при потолке %d", st.IndexRunes, st.IndexLimit)})
	}
	return ps
}
