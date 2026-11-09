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

// Умолчания: в пятерке (так мерили в ADR-0008). Порог 0.40 снят на bge-m3 через
// Ollama (09.11): верные ответы 0.46-0.63, запросы без ответа до 0.32, шум в
// хвосте верных выдач до 0.46. Порог 0.50 у `search` настроен под nomic и верные
// находки здесь отрезал бы.
// ponytail: замер на 6 записях и 7 запросах, одна модель; на настоящем корпусе
// пересмотреть, при необходимости перейти на отсечку относительно лучшего балла.
const (
	DefaultFindLimit     = 5
	DefaultFindThreshold = 0.40
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

// embedNotes embeds the notes the previous index does not cover; unchanged ones are reused.
func (s *Service) embedNotes(texts []NoteText, prev VectorIndex) (VectorIndex, BuildReport) {
	var rep BuildReport
	out := VectorIndex{Model: s.model, Built: s.now().Format(time.RFC3339), Entries: map[string]VectorEntry{}}
	for _, t := range texts {
		old, had := prev.Entries[t.Note.Name]
		had = had && prev.Model == s.model
		if had && old.Digest == t.Note.Digest {
			out.Entries[t.Note.Name] = old
			rep.Reused++
			continue
		}
		v, err := s.embed.Embed(embedText(t))
		if err != nil {
			rep.Failed++
			// Прежний вектор остается: он отстал (check назовет stale-vectors), но
			// запись не выпадает из смыслового поиска из-за одного сбоя службы.
			if had {
				out.Entries[t.Note.Name] = old
			}
			continue
		}
		out.Entries[t.Note.Name] = VectorEntry{Digest: t.Note.Digest, Vector: v}
		rep.Embedded++
	}
	return out, rep
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
	out, rep := s.embedNotes(texts, prev)
	for _, e := range out.Entries {
		out.Dims = len(e.Vector) // размерность одна у всех: индекс снят одной моделью
		break
	}
	if len(out.Entries) == 0 {
		return rep, fmt.Errorf("memory: не снято ни одного вектора (записей %d, отказов %d): индекс не записан", len(texts), rep.Failed)
	}
	return rep, s.store.SaveVectors(out)
}

func (o FindOptions) withDefaults() FindOptions {
	if o.Mode == "" {
		o.Mode = ModeAuto
	}
	if o.Limit <= 0 {
		o.Limit = DefaultFindLimit
	}
	if o.Threshold <= 0 {
		o.Threshold = DefaultFindThreshold
	}
	return o
}

// Find ищет в холодном слое. Журнал и очередь одобрения в поиск не входят.
func (s *Service) Find(query string, o FindOptions) (FindResult, error) {
	o = o.withDefaults()
	texts, err := s.store.ColdTexts()
	if err != nil {
		return FindResult{}, err
	}
	if o.Mode == ModeText {
		return FindResult{Hits: textSearch(texts, query, o.Limit), Via: "text"}, nil
	}
	vi, vErr := s.store.LoadVectors()
	if vErr != nil && !errors.Is(vErr, ErrNoVectors) {
		return FindResult{}, vErr
	}
	if s.embed == nil || vErr != nil || vi.Model != s.model {
		return s.withoutVectors(texts, vi, vErr == nil, query, o)
	}
	return s.findByVector(texts, vi, query, o)
}

// withoutVectors answers when the vector layer cannot: эмбеддера нет, индекс не
// снят или снят другой моделью. Режим vector в этом случае отказывает.
func (s *Service) withoutVectors(texts []NoteText, vi VectorIndex, hasIndex bool, query string, o FindOptions) (FindResult, error) {
	if o.Mode == ModeVector {
		return FindResult{}, search.ErrNoSemanticLayer
	}
	res := FindResult{Hits: textSearch(texts, query, o.Limit), Via: "text"}
	if s.embed != nil && hasIndex { // индекс есть, но снят другой моделью
		res.Notice = fmt.Sprintf("индекс векторов снят моделью %q, а ищем %q: kbengine memory index", vi.Model, s.model)
	}
	return res, nil
}

// findByVector answers from the vector layer and falls back to text only in auto
// mode: служба упала или все записи ниже порога. Причина называется в Notice.
func (s *Service) findByVector(texts []NoteText, vi VectorIndex, query string, o FindOptions) (FindResult, error) {
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
	hits, unindexed := nearest(texts, vi, qv, o)
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

// nearest ranks the notes by closeness to the query vector and counts the notes
// the index does not see (нет вектора или отпечаток разошелся с текстом).
func nearest(texts []NoteText, vi VectorIndex, qv search.Vector, o FindOptions) (hits []Hit, unindexed int) {
	for _, t := range texts {
		e, ok := vi.Entries[t.Note.Name]
		if !ok || e.Digest != t.Note.Digest {
			unindexed++
		}
		if !ok {
			continue
		}
		if sc := search.Cosine(qv, e.Vector); sc >= o.Threshold { // NaN сюда не пройдет
			hits = append(hits, Hit{Note: t.Note, Score: sc})
		}
	}
	sortHits(hits)
	return hits[:min(len(hits), o.Limit)], unindexed
}

// sortHits orders hits by score, best first; равные — по имени, чтобы выдача была устойчивой.
func sortHits(hits []Hit) {
	slices.SortFunc(hits, func(a, b Hit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return strings.Compare(a.Note.Name, b.Note.Name)
	})
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
