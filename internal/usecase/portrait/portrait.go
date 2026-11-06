// Package portrait keeps what DVK remembers about the person (#479): ручные
// правки, разбор обмена моделью в «ждут одобрения», сжатие и текст для чата.
package portrait

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/daniil/kb-engine/internal/domain"
)

// MaxExtractRunes bounds one extraction request: маленькая модель на Маке
// держит 4–8 тыс. токенов на все, а разбор идет рядом с чатом.
// ponytail: пределы в знаках, а не токенах модели. Потолок: на латинице
// токенов меньше, и модель недозагружена. Путь: предел от контекста модели.
const (
	MaxExtractRunes  = maxKnownRunes + maxQuestionRunes + maxAnswerRunes + 100
	maxKnownRunes    = 1500
	maxQuestionRunes = 500
	maxAnswerRunes   = 2000
)

func firstRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func lastRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[len(r)-n:])
	}
	return s
}

// ErrModelReply — модель ответила не тем JSON, который просили.
var ErrModelReply = errors.New("model reply is not the requested JSON")

// ErrConflict — портрет изменили, пока сервис его правил (другое устройство
// на том же kb-сервере): Store.Save отвечает им, сервис перечитывает.
var ErrConflict = errors.New("portrait changed meanwhile")

// conflictAttempts bounds re-reads after ErrConflict: правят двое-трое
// устройств одного человека, и проиграть больше пары раз подряд нельзя.
const conflictAttempts = 5

// Store keeps the portrait: файл в папке человека или kb-сервер.
type Store interface {
	Load(ctx context.Context) (domain.Portrait, error)
	Save(ctx context.Context, p domain.Portrait) error
}

// Model answers one short request: system — правила, user — данные.
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Пометки происхождения факта: человек видит у каждого, откуда он взялся.
const (
	sourceManual     = "добавлено вручную"
	sourceEdited     = "исправлено вручную"
	sourceCompressed = "сжато"
	sourceChat       = "из чата "     // + дата
	sourceEditedTail = ", исправлено" // одобрено с правкой
)

// Service changes the portrait and saves every change.
type Service struct {
	store Store
	model Model
	now   func() time.Time
	newID func() string
	lock  sync.Locker // держит только чтение-правку-запись, не запрос к модели
}

// Option configures a Service.
type Option func(*Service)

// WithLock serializes the changes of one process: файл в папке версий не
// знает, и две правки из одного окна иначе затерли бы друг друга. Запрос к
// модели (разбор, сжатие) идет вне замка.
func WithLock(l sync.Locker) Option { return func(s *Service) { s.lock = l } }

// NewService wires the store, the model, the clock and the id source.
func NewService(store Store, model Model, now func() time.Time, newID func() string, opts ...Option) *Service {
	s := &Service{store: store, model: model, now: now, newID: newID, lock: noLock{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

type noLock struct{}

func (noLock) Lock()   {}
func (noLock) Unlock() {}

// Portrait returns the saved portrait.
func (s *Service) Portrait(ctx context.Context) (domain.Portrait, error) { return s.store.Load(ctx) }

// change applies fn to the stored portrait and saves it; проиграл гонку
// (ErrConflict) — fn применяется заново к свежему портрету.
func (s *Service) change(ctx context.Context, fn func(domain.Portrait) (domain.Portrait, error)) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	var err error
	for range conflictAttempts {
		var p domain.Portrait
		if p, err = s.store.Load(ctx); err != nil {
			return err
		}
		if p, err = fn(p); err != nil {
			return err
		}
		if err = s.store.Save(ctx, p); !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return err
}

// Add adds a fact typed by the person: одобрен сразу.
func (s *Service) Add(ctx context.Context, section domain.FactSection, text string) (domain.Fact, error) {
	f, err := domain.NewFact(s.newID(), section, text, sourceManual, domain.FactApproved, s.now())
	if err != nil {
		return domain.Fact{}, err
	}
	return f, s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) { return p.Add(f) })
}

// Propose puts a fact the model offered in the chat to «ждут одобрения».
func (s *Service) Propose(ctx context.Context, section domain.FactSection, text string) (domain.Fact, error) {
	f, err := domain.NewFact(s.newID(), section, text, sourceChat+s.now().Format("02.01"), domain.FactPending, s.now())
	if err != nil {
		return domain.Fact{}, err
	}
	return f, s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) { return p.Add(f) })
}

// Restore puts a forgotten fact back as it was: id, источник, дата, одобрение.
func (s *Service) Restore(ctx context.Context, f domain.Fact) error {
	return s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) { return p.Add(f) })
}

// Edit changes the fact text.
func (s *Service) Edit(ctx context.Context, id, text string) error {
	return s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) { return p.Edit(id, text, sourceEdited) })
}

// Remove forgets the fact.
func (s *Service) Remove(ctx context.Context, id string) error {
	return s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) { return p.Remove(id) })
}

// Approve lets the fact into the chat; text — правка перед одобрением, пусто — как есть.
func (s *Service) Approve(ctx context.Context, id, text string) error {
	return s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) {
		if strings.TrimSpace(text) != "" {
			for _, f := range p.Facts() {
				if f.ID() == id && strings.TrimSpace(text) != f.Text() {
					var err error
					if p, err = p.Edit(id, text, f.Source()+sourceEditedTail); err != nil {
						return p, err
					}
				}
			}
		}
		return p.Approve(id)
	})
}

// proposal is a fact the model offers: раздел и текст.
type proposal struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// Proposal is a fact of the compressed portrait the person may accept.
type Proposal struct {
	Section domain.FactSection
	Text    string
}

const extractPrompt = `Ты ведешь портрет человека: устойчивые факты о нем самом — кто он, работа, планы, предпочтения в общении и привычки.
По обмену ниже найди НОВЫЕ такие факты. Не бери содержание документов, разовые просьбы и то, что уже известно.
Ответь только JSON-массивом, без пояснений: [{"section":"about|work|plans|prefs|misc","text":"факт одной фразой от третьего лица"}].
Нового нет — ответь [].`

// Extract finds new facts in one exchange and puts them to «ждут одобрения».
func (s *Service) Extract(ctx context.Context, question, answer string) ([]domain.Fact, error) {
	p, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	known := domain.PortraitText(p.Facts())
	if known == "" {
		known = "ничего"
	}
	// Известное — с конца (свежее важнее), вопрос и ответ — с начала.
	user := "Уже известно:\n" + lastRunes(known, maxKnownRunes) + "\n\nВопрос человека:\n" + firstRunes(question, maxQuestionRunes) +
		"\n\nОтвет:\n" + firstRunes(answer, maxAnswerRunes)
	reply, err := s.model.Complete(ctx, extractPrompt, user)
	if err != nil {
		return nil, err
	}
	offered, err := parseProposals(reply)
	if err != nil {
		return nil, err
	}
	var candidates []domain.Fact
	src := sourceChat + s.now().Format("02.01")
	for _, o := range offered {
		sec, err := domain.NewFactSection(o.Section)
		if err != nil {
			continue // чужой раздел — модель ошиблась, факт мимо
		}
		if f, err := domain.NewFact(s.newID(), sec, o.Text, src, domain.FactPending, s.now()); err == nil {
			candidates = append(candidates, f)
		}
	}
	var added []domain.Fact
	err = s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) {
		added = nil
		for _, f := range candidates {
			if next, err := p.Add(f); err == nil { // ошибка — уже известно
				p, added = next, append(added, f)
			}
		}
		if len(added) == 0 {
			return p, errNothingNew
		}
		return p, nil
	})
	if errors.Is(err, errNothingNew) {
		return nil, nil
	}
	return added, err
}

// errNothingNew stops change without a save: в обмене нового нет.
var errNothingNew = errors.New("nothing new")

const compressPrompt = `Сожми портрет человека: объедини похожие факты, убери повторы и мелочи, сохрани суть.
Каждый раздел — не больше двух коротких фактов. Ответь только JSON-массивом: [{"section":"about|work|plans|prefs|misc","text":"..."}].`

// Compress asks the model for a shorter portrait; применяет ApplyCompressed — по согласию человека.
func (s *Service) Compress(ctx context.Context) (Compression, error) {
	p, err := s.store.Load(ctx)
	if err != nil {
		return Compression{}, err
	}
	reply, err := s.model.Complete(ctx, compressPrompt, domain.PortraitText(p.Approved()))
	if err != nil {
		return Compression{}, err
	}
	offered, err := parseProposals(reply)
	if err != nil {
		return Compression{}, err
	}
	var c Compression
	var facts []domain.Fact
	for _, o := range offered {
		sec, err := domain.NewFactSection(o.Section)
		if err != nil {
			continue
		}
		f, err := domain.NewFact("size", sec, o.Text, sourceCompressed, domain.FactApproved, s.now())
		if err != nil {
			continue
		}
		c.Proposals, facts = append(c.Proposals, Proposal{Section: sec, Text: f.Text()}), append(facts, f)
	}
	if len(c.Proposals) == 0 {
		return Compression{}, fmt.Errorf("%w: empty compression", ErrModelReply)
	}
	for _, f := range p.Approved() {
		c.Basis = append(c.Basis, f.ID())
	}
	c.Size = len([]rune(domain.PortraitText(facts)))
	return c, nil
}

// Compression is a shorter portrait the person may accept: предложение,
// размер его текста в чате и id одобренных фактов, из которых оно сжато.
type Compression struct {
	Proposals []Proposal
	Basis     []string
	Size      int
}

// ApplyCompressed replaces the facts the proposal was made from; одобренное
// после «Сжать» и ждущее одобрения остается.
func (s *Service) ApplyCompressed(ctx context.Context, c Compression) error {
	return s.change(ctx, func(p domain.Portrait) (domain.Portrait, error) {
		next := p
		for _, id := range c.Basis {
			var err error
			if next, err = next.Remove(id); err != nil && !errors.Is(err, domain.ErrFactNotFound) {
				return p, err
			}
		}
		for _, pr := range c.Proposals {
			f, err := domain.NewFact(s.newID(), pr.Section, pr.Text, sourceCompressed, domain.FactApproved, s.now())
			if err != nil {
				return p, err
			}
			if next, err = next.Add(f); err != nil && !errors.Is(err, domain.ErrFactExists) {
				return p, err
			}
		}
		return next, nil
	})
}

// parseProposals reads the model JSON, срезая обертку ``` если она есть.
func parseProposals(reply string) ([]proposal, error) {
	r := strings.TrimSpace(reply)
	if i, j := strings.Index(r, "["), strings.LastIndex(r, "]"); i >= 0 && j > i {
		r = r[i : j+1]
	}
	var out []proposal
	if err := json.Unmarshal([]byte(r), &out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrModelReply, err)
	}
	return out, nil
}

// ContextMode — как портрет идет в чат, когда не помещается.
type ContextMode string

// Режимы: Auto — целиком, если помещается, иначе ничего, пока человек не выбрал.
const (
	ContextAuto     ContextMode = ""
	ContextFull     ContextMode = "full"
	ContextRelevant ContextMode = "relevant"
)

// ChatContext is the portrait for one chat: текст, полный размер и помещается ли.
type ChatContext struct {
	Text string
	Size int // знаков в полном портрете
	Fits bool
}

// Context returns the portrait text for a chat within budget runes.
func Context(p domain.Portrait, question string, budget int, mode ContextMode) ChatContext {
	full := domain.PortraitText(p.Approved())
	c := ChatContext{Size: len([]rune(full)), Fits: len([]rune(full)) <= budget}
	switch {
	case c.Fits || mode == ContextFull:
		c.Text = full
	case mode == ContextRelevant:
		facts := relevant(p.Approved(), question)
		// «Только нужное» тоже в пределах порога: лишнее — с конца.
		for len(facts) > 0 && len([]rune(domain.PortraitText(facts))) > budget {
			facts = facts[:len(facts)-1]
		}
		c.Text = domain.PortraitText(facts)
	}
	return c
}

// relevant keeps «кто я» и предпочтения всегда, остальное — по общим словам с вопросом.
// ponytail: совпадение по первым пяти буквам слова. Потолок: синонимы
// («переезд» и «квартира») не находятся. Путь: отбор моделью или поиском окна.
func relevant(facts []domain.Fact, question string) []domain.Fact {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(question, notLetter) {
		if st := stem(w); len([]rune(st)) >= 4 {
			words[st] = true
		}
	}
	var out []domain.Fact
	for _, f := range facts {
		keep := f.Section() == domain.SectionAbout || f.Section() == domain.SectionPrefs
		for _, w := range strings.FieldsFunc(f.Text(), notLetter) {
			keep = keep || words[stem(w)]
		}
		if keep {
			out = append(out, f)
		}
	}
	return out
}

func notLetter(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

func stem(w string) string {
	r := []rune(strings.ToLower(w))
	if len(r) > 5 {
		r = r[:5]
	}
	return string(r)
}
