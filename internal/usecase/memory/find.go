package memory

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// ErrNoVectors — индекс векторов еще не снят: слой необязателен, это не поломка.
var ErrNoVectors = errors.New("memory: индекс векторов не снят")

// NoteText — запись холодного слоя вместе с телом: то, по чему ищут.
type NoteText struct {
	Note domain.ColdNote
	Body string
}

// VectorEntry — вектор записи и отпечаток текста, с которого он снят.
type VectorEntry struct {
	Digest string
	Vector search.Vector
}

// VectorIndex — векторы холодного слоя. Модель и размерность хранятся вместе с
// ними: индекс, снятый другой моделью, состоит из таких же чисел и молча дает шум.
type VectorIndex struct {
	Model   string
	Dims    int
	Built   string
	Entries map[string]VectorEntry
}

// FindMode — каким слоем искать.
type FindMode string

// Режимы поиска. Auto: вектор первым, текст запасным; смесь не делается —
// равная смесь хуже одного вектора (36/40 против 39/40, ADR-0008).
const (
	ModeAuto   FindMode = "auto"
	ModeText   FindMode = "text"
	ModeVector FindMode = "vector"
)

// Умолчания: в пятерке (так мерили в ADR-0008); порог — как у `search`.
const (
	DefaultFindLimit     = 5
	DefaultFindThreshold = 0.50
	// maxEmbedRunes — сколько знаков записи уходит в эмбеддер.
	// ponytail: длинное тело обрезается, хвост записи вектору не виден; путь вверх —
	// резать на абзацы и брать лучший.
	maxEmbedRunes = 2000
)

// ParseFindMode reads --mode.
func ParseFindMode(s string) (FindMode, error) {
	switch FindMode(s) {
	case "", ModeAuto:
		return ModeAuto, nil
	case ModeText, ModeVector:
		return FindMode(s), nil
	}
	return "", fmt.Errorf("%w: режим %q — auto, text или vector", domain.ErrInvalidNote, s)
}

// FindOptions — параметры поиска; нули берут умолчания.
type FindOptions struct {
	Mode      FindMode
	Limit     int
	Threshold float64
}

// Hit — найденная запись, одна на выдачу.
type Hit struct {
	Note  domain.ColdNote
	Score float64
}

// FindResult — выдача и то, каким слоем она получена. Notice непуст, когда слой
// не тот, что ждали: пустота вместо ответа читается как «в базе такого нет».
type FindResult struct {
	Hits   []Hit
	Via    string
	Notice string
}

// BuildReport — итог снятия индекса.
type BuildReport struct {
	Embedded, Reused, Failed int
}

func embedText(n NoteText) string {
	t := strings.Join([]string{n.Note.Name, n.Note.Topic, n.Note.Description, n.Body}, "\n")
	if utf8.RuneCountInString(t) > maxEmbedRunes {
		t = string([]rune(t)[:maxEmbedRunes])
	}
	return t
}

// BuildIndex снимает векторы холодного слоя. Записи с прежним отпечатком и той же
// моделью переиспользуются, остальные считаются заново; отказ на одной записи не
// бросает индекс, но считается и называется.
func (s *Service) BuildIndex() (BuildReport, error) {
	var rep BuildReport
	if s.embed == nil {
		return rep, search.ErrNoSemanticLayer
	}
	texts, err := s.store.ColdTexts()
	if err != nil {
		return rep, err
	}
	prev, err := s.store.LoadVectors()
	if err != nil && !errors.Is(err, ErrNoVectors) {
		return rep, err
	}
	out := VectorIndex{Model: s.model, Built: s.now().Format(time.RFC3339), Entries: map[string]VectorEntry{}}
	for _, t := range texts {
		if e, ok := prev.Entries[t.Note.Name]; ok && prev.Model == s.model && e.Digest == t.Note.Digest {
			out.Entries[t.Note.Name] = e
			rep.Reused++
			continue
		}
		v, err := s.embed.Embed(embedText(t))
		if err != nil {
			rep.Failed++
			continue
		}
		out.Entries[t.Note.Name] = VectorEntry{Digest: t.Note.Digest, Vector: v}
		if out.Dims == 0 {
			out.Dims = len(v)
		}
		rep.Embedded++
	}
	if len(out.Entries) == 0 {
		return rep, fmt.Errorf("memory: не снято ни одного вектора (записей %d, отказов %d): индекс не записан", len(texts), rep.Failed)
	}
	return rep, s.store.SaveVectors(out)
}

// Find ищет в холодном слое. Журнал и очередь одобрения в поиск не входят.
func (s *Service) Find(query string, o FindOptions) (FindResult, error) {
	if o.Mode == "" {
		o.Mode = ModeAuto
	}
	if o.Limit <= 0 {
		o.Limit = DefaultFindLimit
	}
	if o.Threshold <= 0 {
		o.Threshold = DefaultFindThreshold
	}
	texts, err := s.store.ColdTexts()
	if err != nil {
		return FindResult{}, err
	}
	if o.Mode == ModeText {
		return FindResult{Hits: textSearch(texts, query, o.Limit), Via: "text"}, nil
	}

	vi, vErr := s.store.LoadVectors()
	usable := s.embed != nil && vErr == nil && vi.Model == s.model
	if vErr != nil && !errors.Is(vErr, ErrNoVectors) {
		return FindResult{}, vErr
	}
	if !usable {
		if o.Mode == ModeVector {
			return FindResult{}, search.ErrNoSemanticLayer
		}
		res := FindResult{Hits: textSearch(texts, query, o.Limit), Via: "text"}
		if s.embed != nil && vErr == nil { // индекс есть, но снят другой моделью
			res.Notice = fmt.Sprintf("индекс векторов снят моделью %q, а ищем %q: kbengine memory index", vi.Model, s.model)
		}
		return res, nil
	}

	qv, err := s.embed.Embed(query)
	if err != nil {
		if o.Mode == ModeVector {
			return FindResult{}, fmt.Errorf("memory: вектор запроса не получен: %w", err)
		}
		return FindResult{
			Hits: textSearch(texts, query, o.Limit), Via: "text",
			Notice: fmt.Sprintf("смысловой слой недоступен: %v; ответ по тексту", err),
		}, nil
	}

	byName := make(map[string]domain.ColdNote, len(texts))
	unindexed := 0
	for _, t := range texts {
		byName[t.Note.Name] = t.Note
		if e, ok := vi.Entries[t.Note.Name]; !ok || e.Digest != t.Note.Digest {
			unindexed++
		}
	}
	var hits []Hit
	for name, e := range vi.Entries {
		n, ok := byName[name]
		if !ok {
			continue
		}
		if sc := search.Cosine(qv, e.Vector); sc >= o.Threshold { // NaN сюда не пройдет
			hits = append(hits, Hit{Note: n, Score: sc})
		}
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return strings.Compare(a.Note.Name, b.Note.Name)
	})
	if len(hits) > o.Limit {
		hits = hits[:o.Limit]
	}

	var notes []string
	if unindexed > 0 {
		notes = append(notes, fmt.Sprintf("%d %s без свежего вектора, их вектор не видит: kbengine memory index", unindexed, plural(unindexed, "запись", "записи", "записей")))
	}
	if len(hits) > 0 || o.Mode == ModeVector {
		return FindResult{Hits: hits, Via: "vector", Notice: strings.Join(notes, "; ")}, nil
	}
	notes = append([]string{fmt.Sprintf("смысловой слой: все записи ниже порога %.2f, ответ по тексту", o.Threshold)}, notes...)
	return FindResult{Hits: textSearch(texts, query, o.Limit), Via: "text", Notice: strings.Join(notes, "; ")}, nil
}

// plural выбирает форму существительного по числу: 1 запись, 2 записи, 5 записей.
func plural(n int, one, few, many string) string {
	switch m10, m100 := n%10, n%100; {
	case m10 == 1 && m100 != 11:
		return one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return few
	}
	return many
}
