package domain

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
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

// ErrNoTopicMarkers — в HOT.md нет пары меток перечня тем: файл правили руками, и
// переписывать его вслепую нельзя.
var ErrNoTopicMarkers = errors.New("HOT.md has no topic list markers")

// ErrNoSuchPending — в очереди одобрения нет записи с таким именем.
var ErrNoSuchPending = errors.New("no such pending memory note")

// ErrHotFull — правило не помещается в горячий слой: потолок не обходится одобрением.
var ErrHotFull = errors.New("hot layer would exceed its limit")

// ErrNoRulesSection — в HOT.md нет раздела правил, и дописывать правило некуда.
var ErrNoRulesSection = errors.New("HOT.md has no rules section")

// RulesHeading — заголовок раздела правил в HOT.md, куда `approve --to hot` дописывает.
const RulesHeading = "## Правила работы"

// ErrNoPortraitMarkers — в HOT.md нет пары меток блока «Кто я».
var ErrNoPortraitMarkers = errors.New("HOT.md has no portrait markers")

// ErrInvalidLimits — потолок горячего слоя или индекса вне разумного.
var ErrInvalidLimits = errors.New("invalid memory limits")

// Границы потолков в знаках: ниже горячий слой не вмещает и скелет, выше он перестает
// быть «горячим» (ADR-0008: потолок по качеству не найден, выбирают по цене токенов).
const (
	MinHotLimit   = 200
	MaxHotLimit   = 200000
	MinIndexLimit = 1000
	MaxIndexLimit = 1000000
)

// ValidateLimits checks the ceilings a person sets; 0 — «не задан, брать умолчание».
func ValidateLimits(hot, index int) error {
	if hot != 0 && (hot < MinHotLimit || hot > MaxHotLimit) {
		return fmt.Errorf("%w: горячий слой %d знаков, допустимо %d-%d", ErrInvalidLimits, hot, MinHotLimit, MaxHotLimit)
	}
	if index != 0 && (index < MinIndexLimit || index > MaxIndexLimit) {
		return fmt.Errorf("%w: индекс %d знаков, допустимо %d-%d", ErrInvalidLimits, index, MinIndexLimit, MaxIndexLimit)
	}
	return nil
}

// DefaultHotLimit — потолок горячего слоя в знаках.
const DefaultHotLimit = 4000

// DefaultIndexLimit — потолок cold/INDEX.md в знаках. Индекс в сессию не грузится,
// это обзор для человека: разросшийся индекс перестает быть обзором.
const DefaultIndexLimit = 30000

// MaxHookRunes — потолок крючка: одна строка индекса.
const MaxHookRunes = 150

// MaxTopicRunes — потолок названия темы: она попадает в перечень тем горячего слоя.
const MaxTopicRunes = 40

// Метки блока «Кто я» в HOT.md: между ними `kbengine memory who` держит копию
// одобренных фактов портрета DVK (ADR-0007). Источник правды один — портрет;
// блок производный и руками не правится.
const (
	PortraitStart = "<!-- portrait:start -->"
	PortraitEnd   = "<!-- portrait:end -->"
)

// Метки блока «перечень тем» в HOT.md: между ними `kbengine memory topics` держит
// список тем холодного слоя (ADR-0008: без перечня правила без слов-триггеров теряются).
const (
	TopicsStart = "<!-- topics:start -->"
	TopicsEnd   = "<!-- topics:end -->"
)

// Origin says who wrote a note: человек или агент (запись агента ждет одобрения).
type Origin string

// Авторы записей.
const (
	OriginHuman Origin = "human"
	OriginAgent Origin = "agent"
)

// ParseOrigin reads a flag value; пустое значение — человек.
func ParseOrigin(s string) (Origin, error) {
	switch Origin(s) {
	case "", OriginHuman:
		return OriginHuman, nil
	case OriginAgent:
		return OriginAgent, nil
	}
	return "", fmt.Errorf("%w: origin %q — human или agent", ErrInvalidNote, s)
}

// Layer — слой, куда одобряется запись из очереди.
type Layer string

// Слои памяти.
const (
	LayerCold Layer = "cold"
	LayerHot  Layer = "hot"
)

// ParseLayer reads --to.
func ParseLayer(s string) (Layer, error) {
	switch Layer(s) {
	case LayerCold, LayerHot:
		return Layer(s), nil
	}
	return "", fmt.Errorf("%w: слой %q — cold или hot", ErrInvalidNote, s)
}

// ValidNoteName says whether name can be a note name (and so a file name):
// проверка на границе, чтобы имя из флага не вело за пределы папки памяти.
func ValidNoteName(name string) bool { return noteName.MatchString(name) }

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
	ProblemStaleTopics   ProblemKind = "stale-topics"
	ProblemStaleVectors  ProblemKind = "stale-vectors"
	ProblemInvalidNote   ProblemKind = "invalid-note"
	ProblemHotChanged    ProblemKind = "hot-changed-outside"
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
	Origin      Origin
	Description string
	// Digest — отпечаток файла записи: по нему видно, что вектор снят с другого текста.
	Digest string
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
	// HasVectors — снят ли индекс векторов (слой необязателен); VectorDigests —
	// имя записи → отпечаток, с которого вектор снят.
	HasVectors    bool
	VectorDigests map[string]string
	// HotDigest — отпечаток HOT.md сейчас; SealDigest — отпечаток после последней
	// записи системы или подтверждения человеком ("" — печати нет, это не нарушение).
	HotDigest, SealDigest string
}

// IndexEntry — строка индекса холодной памяти.
type IndexEntry struct {
	File string
	Hook string
}

var noteName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// noMarkup refuses the comment markers that delimit the generated blocks of HOT.md:
// текст с ними мог бы закрыть блок раньше срока или подделать метки, а HOT.md читает
// каждая сессия агента.
func noMarkup(what, s string) error {
	if strings.Contains(s, "<!--") || strings.Contains(s, "-->") {
		return fmt.Errorf("%w: %s содержит разметку комментария", ErrInvalidNote, what)
	}
	return nil
}

// validateFields checks the parts of a note that reach HOT.md and the index.
func validateFields(topic, description string) error {
	switch {
	case topic == "":
		return fmt.Errorf("%w: тема пустая", ErrInvalidNote)
	case strings.ContainsAny(topic, "\r\n"):
		return fmt.Errorf("%w: тема в одну строку", ErrInvalidNote)
	case utf8.RuneCountInString(topic) > MaxTopicRunes:
		return fmt.Errorf("%w: тема длиннее %d знаков", ErrInvalidNote, MaxTopicRunes)
	case description == "":
		return fmt.Errorf("%w: описание пустое", ErrInvalidNote)
	case strings.ContainsAny(description, "\r\n"):
		return fmt.Errorf("%w: описание в одну строку", ErrInvalidNote)
	case utf8.RuneCountInString(description) > MaxHookRunes:
		return fmt.Errorf("%w: описание длиннее %d знаков", ErrInvalidNote, MaxHookRunes)
	}
	if err := noMarkup("тема", topic); err != nil {
		return err
	}
	return noMarkup("описание", description)
}

// ValidateNote re-checks a note that did not come through NewColdNote: файл могли
// положить руками или агент в обход инструмента (ревью #521).
func ValidateNote(n ColdNote) error {
	return validateFields(strings.TrimSpace(n.Topic), strings.TrimSpace(n.Description))
}

// NewColdNote validates a new cold note: имя — kebab-латиница (оно же имя файла),
// тема — одна строка до MaxTopicRunes (по ней собирается перечень тем горячего слоя),
// описание — одна строка до MaxHookRunes, потому что оно же крючок в индексе.
func NewColdNote(name, topic, description string) (ColdNote, error) {
	topic = strings.TrimSpace(topic)
	description = strings.TrimSpace(description)
	if !noteName.MatchString(name) {
		return ColdNote{}, fmt.Errorf("%w: имя %q — латиница в нижнем регистре, цифры и дефисы", ErrInvalidNote, name)
	}
	if err := validateFields(topic, description); err != nil {
		return ColdNote{}, err
	}
	return ColdNote{File: name + ".md", Name: name, Topic: topic, Description: description}, nil
}

// AppendRule adds one rule as a bullet at the end of the rules section of the hot
// layer; пустой маркер «-» из скелета заменяется, а не копится. То же правило
// второй раз не дописывается: текст возвращается как был.
func AppendRule(hot, rule string) (string, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" || strings.ContainsAny(rule, "\r\n") {
		return "", fmt.Errorf("%w: правило — одна непустая строка", ErrInvalidNote)
	}
	if err := noMarkup("правило", rule); err != nil {
		return "", err
	}
	lines := strings.Split(hot, "\n")
	h := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == RulesHeading })
	if h < 0 {
		return "", ErrNoRulesSection
	}
	last := lastBullet(lines, h)
	bullet := "- " + rule
	for i := h + 1; i <= last; i++ {
		if strings.EqualFold(strings.TrimSpace(lines[i]), bullet) {
			return hot, nil
		}
	}
	switch {
	case last >= 0 && strings.TrimSpace(lines[last]) == "-":
		lines[last] = bullet
	case last >= 0:
		lines = slices.Insert(lines, last+1, bullet)
	default:
		lines = slices.Insert(lines, h+1, bullet)
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

// lastBullet returns the index of the last bullet in the section that starts at
// the heading line h, or -1; раздел кончается заголовком «## » или концом файла.
func lastBullet(lines []string, h int) int {
	last := -1
	for i := h + 1; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		if l := strings.TrimSpace(lines[i]); l == "-" || strings.HasPrefix(l, "- ") {
			last = i
		}
	}
	return last
}

// TopicList returns the distinct topics of the notes, alphabetical, one spelling per
// topic (первое встреченное); записи без темы пропускаются.
func TopicList(notes []ColdNote) []string {
	seen := make(map[string]bool, len(notes))
	var out []string
	for _, n := range notes {
		t := strings.TrimSpace(n.Topic)
		if k := strings.ToLower(t); t != "" && !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// cutBlock cuts the text between a pair of markers; ok=false — меток нет или они не по порядку.
func cutBlock(hot, start, end string) (before, body, after string, ok bool) {
	before, rest, ok := strings.Cut(hot, start)
	if !ok {
		return "", "", "", false
	}
	body, after, ok = strings.Cut(rest, end)
	return before, body, after, ok
}

func topicsBlock(hot string) (before, body, after string, ok bool) {
	return cutBlock(hot, TopicsStart, TopicsEnd)
}

func topicLines(notes []ColdNote) []string {
	topics := TopicList(notes)
	for i, t := range topics {
		topics[i] = "- " + t
	}
	return topics
}

// ReplaceTopics rewrites the block between the markers with the notes' topics,
// everything else in the hot layer stays byte for byte.
func ReplaceTopics(hot string, notes []ColdNote) (string, error) {
	before, _, after, ok := topicsBlock(hot)
	if !ok {
		return "", ErrNoTopicMarkers
	}
	for _, n := range notes {
		if err := noMarkup("тема", n.Topic); err != nil {
			return "", err
		}
	}
	lines := strings.Join(topicLines(notes), "\n")
	if lines != "" {
		lines += "\n"
	}
	return before + TopicsStart + "\n" + lines + TopicsEnd + after, nil
}

// ReplacePortrait rewrites the «Кто я» block with the portrait text, everything else
// stays byte for byte. Разметка комментария в тексте отвергается: текст из портрета
// не должен уметь закрыть блок раньше срока или подделать метки перечня тем.
func ReplacePortrait(hot, text string) (string, error) {
	if strings.Contains(text, "<!--") || strings.Contains(text, "-->") {
		return "", fmt.Errorf("%w: текст портрета содержит разметку комментария", ErrInvalidNote)
	}
	before, _, after, ok := cutBlock(hot, PortraitStart, PortraitEnd)
	if !ok {
		return "", ErrNoPortraitMarkers
	}
	text = strings.TrimSpace(text)
	if text != "" {
		text += "\n"
	}
	return before + PortraitStart + "\n" + text + PortraitEnd + after, nil
}

// HasTopicList says whether the hot layer carries a non-empty topic list block.
func HasTopicList(hot string) bool {
	_, body, _, ok := topicsBlock(hot)
	return ok && strings.TrimSpace(body) != ""
}

// topicListIsCurrent compares the block with what the notes' topics would produce.
func topicListIsCurrent(hot string, notes []ColdNote) bool {
	_, body, _, _ := topicsBlock(hot)
	var have []string
	for l := range strings.SplitSeq(body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			have = append(have, l)
		}
	}
	return slices.Equal(have, topicLines(notes))
}

// CheckMemory returns every violation of the layout, in a stable order:
// горячий слой, записи холодного, индекс, векторы, размер индекса.
func CheckMemory(st MemoryState) []MemoryProblem {
	ps := checkHot(st)
	ps = append(ps, checkSeal(st)...)
	ps = append(ps, checkNotes(st)...)
	ps = append(ps, checkIndex(st)...)
	ps = append(ps, checkVectors(st)...)
	if st.IndexRunes > st.IndexLimit {
		ps = append(ps, MemoryProblem{ProblemIndexTooBig, "cold/INDEX.md", fmt.Sprintf("%d знаков при потолке %d", st.IndexRunes, st.IndexLimit)})
	}
	return ps
}

func checkHot(st MemoryState) []MemoryProblem {
	var ps []MemoryProblem
	if n := utf8.RuneCountInString(st.Hot); n > st.HotLimit {
		ps = append(ps, MemoryProblem{ProblemHotTooBig, "HOT.md", fmt.Sprintf("%d знаков при лимите %d", n, st.HotLimit)})
	}
	if len(st.Notes) > 0 && !HasTopicList(st.Hot) {
		ps = append(ps, MemoryProblem{ProblemNoTopicList, "HOT.md", "записи есть, перечня тем между " + TopicsStart + " и " + TopicsEnd + " нет: kbengine memory topics"})
	}
	if len(st.Notes) > 0 && HasTopicList(st.Hot) && !topicListIsCurrent(st.Hot, st.Notes) {
		ps = append(ps, MemoryProblem{ProblemStaleTopics, "HOT.md", "перечень тем не совпадает с темами записей: kbengine memory topics"})
	}
	return ps
}

// HotDigest is the fingerprint of the hot layer's text; по нему видно правку мимо системы.
func HotDigest(hot string) string {
	sum := sha256.Sum256([]byte(hot))
	return hex.EncodeToString(sum[:])
}

func checkSeal(st MemoryState) []MemoryProblem {
	if st.SealDigest == "" || st.SealDigest == st.HotDigest {
		return nil
	}
	return []MemoryProblem{{ProblemHotChanged, "HOT.md", "файл изменен не окном и не kbengine после последней записи системы: прочитайте его и подтвердите (kbengine memory seal)"}}
}

func checkNotes(st MemoryState) []MemoryProblem {
	indexed := make(map[string]bool, len(st.Index))
	for _, e := range st.Index {
		indexed[e.File] = true
	}
	var ps []MemoryProblem
	for _, n := range st.Notes {
		if strings.TrimSpace(n.Description) == "" {
			ps = append(ps, MemoryProblem{ProblemNoDescription, n.File, "в шапке нет description"})
		}
		if strings.TrimSpace(n.Topic) == "" {
			ps = append(ps, MemoryProblem{ProblemNoTopic, n.File, "в шапке нет topic"})
		}
		if !indexed[n.File] {
			ps = append(ps, MemoryProblem{ProblemNotIndexed, n.File, "нет строки в cold/INDEX.md"})
		}
		if n.Topic != "" && n.Description != "" {
			if err := ValidateNote(n); err != nil {
				ps = append(ps, MemoryProblem{ProblemInvalidNote, n.File, err.Error()})
			}
		}
	}
	return ps
}

func checkIndex(st MemoryState) []MemoryProblem {
	exists := make(map[string]bool, len(st.Notes))
	for _, n := range st.Notes {
		exists[n.File] = true
	}
	var ps []MemoryProblem
	for _, e := range st.Index {
		if !exists[e.File] {
			ps = append(ps, MemoryProblem{ProblemDangling, e.File, "индекс ведет на файл, которого нет"})
		}
		if n := utf8.RuneCountInString(e.Hook); n > MaxHookRunes {
			ps = append(ps, MemoryProblem{ProblemLongHook, e.File, fmt.Sprintf("крючок %d знаков при потолке %d", n, MaxHookRunes)})
		}
	}
	return ps
}

// checkVectors сверяет отпечатки: индекс векторов необязателен, но если он есть,
// он не должен отстать от текста записей.
func checkVectors(st MemoryState) []MemoryProblem {
	if !st.HasVectors {
		return nil
	}
	names := make(map[string]bool, len(st.Notes))
	stale := 0
	for _, n := range st.Notes {
		names[n.Name] = true
		if d, ok := st.VectorDigests[n.Name]; !ok || d != n.Digest {
			stale++
		}
	}
	for name := range st.VectorDigests {
		if !names[name] {
			stale++
		}
	}
	if stale == 0 {
		return nil
	}
	return []MemoryProblem{{ProblemStaleVectors, "cold/.vectors.json", fmt.Sprintf("записей без свежего вектора или с лишним: %d, kbengine memory index", stale)}}
}
