package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/embedhttp"
	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Экран «Память» (ADR-0008): два слоя и журнал рядом, очередь на одобрение,
// потолок горячего слоя. Окно показывает те же файлы, что читает терминал
// (`kbengine memory …`); ни своего хранилища, ни своих правил у него нет.

// memoryDesk is the memory screen over one folder of the person.
type memoryDesk struct {
	dir  string
	auth authHelper // Touch ID для изменений горячего слоя; nil — окно не из сборки, помощника нет
	mu   sync.Mutex // изменения идут по одному: одобрение и потолок читают-пишут один HOT.md
	// embed — служба эмбеддингов для «Пересобрать векторы»; nil — локальная Ollama по умолчанию.
	embed search.Embedder
}

func newMemoryDesk(home string) *memoryDesk { return &memoryDesk{dir: memorydir.DefaultPath(home)} }

// service builds the use case with the ceilings this memory keeps in memory.json.
func (d *memoryDesk) service() (*memory.Service, *memorydir.Store, error) {
	st := memorydir.New(d.dir)
	lim, err := st.Limits()
	if err != nil {
		return nil, nil, err
	}
	return memory.New(st, lim), st, nil
}

type hotView struct {
	Text   string   `json:"text"`
	Runes  int      `json:"runes"`
	Tokens int      `json:"tokens"` // на глаз: три знака на токен
	Limit  int      `json:"limit"`
	Topics []string `json:"topics"` // темы холодного слоя, как их соберет `memory topics`
}

type noteView struct {
	Name        string `json:"name"`
	Topic       string `json:"topic"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
	Body        string `json:"body,omitempty"`     // только у очереди: решать надо по тексту
	WaitDays    int    `json:"waitDays,omitempty"` // только у очереди: сколько дней предложение ждёт
}

// healthView is the health of the memory as the screen draws it (ADR-0008). Тексты
// причин и названия частей собирает страница: Go отдаёт виды и числа.
type healthView struct {
	Mode              string       `json:"mode"` // ok, warn, bad
	Total             int          `json:"total"`
	IdleDays          int          `json:"idleDays"` // -1: суждений в журнале нет
	OldestPendingDays int          `json:"oldestPendingDays"`
	StaleRules        int          `json:"staleRules"`
	Today             string       `json:"today"` // YYYY-MM-DD: от этого дня считались возрасты
	Parts             []partView   `json:"parts"`
	Reasons           []reasonView `json:"reasons"`
	Rules             []ruleView   `json:"rules"`
	Events            []eventView  `json:"events"` // все решения журнала, старые первыми
}

type partView struct {
	ID    string  `json:"id"`
	Value float64 `json:"value"`
	Max   float64 `json:"max"`
}

type reasonView struct {
	Kind string `json:"kind"`
	N    int    `json:"n"`
}

type ruleView struct {
	Text    string `json:"text"`
	AgeDays int    `json:"ageDays"` // -1: отметки нет совсем
	Stale   bool   `json:"stale"`
}

type eventView struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Judgment bool   `json:"judgment"` // суждение человека, а не обслуживание
}

const dayLayout = "2006-01-02"

func newHealthView(h domain.Health, events []domain.Decision, today time.Time) healthView {
	v := healthView{
		Mode: string(h.Mode), Total: h.Total, IdleDays: h.IdleDays, OldestPendingDays: h.OldestPendingDays,
		StaleRules: h.StaleRules, Today: today.Format(dayLayout),
		Parts: []partView{}, Reasons: []reasonView{}, Rules: []ruleView{}, Events: []eventView{},
	}
	for _, p := range h.Parts {
		v.Parts = append(v.Parts, partView{p.ID, p.Value, p.Max})
	}
	for _, r := range h.Reasons {
		v.Reasons = append(v.Reasons, reasonView{string(r.Kind), r.N})
	}
	for _, r := range h.Rules {
		v.Rules = append(v.Rules, ruleView{r.Text, r.AgeDays, r.Stale})
	}
	for _, e := range events {
		v.Events = append(v.Events, eventView{e.Day.Format(dayLayout), string(e.Kind), e.Text, e.Kind.IsJudgment()})
	}
	return v
}

type problemView struct {
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Detail string `json:"detail"`
}

// memoryView is the whole screen: слои, очередь, журнал, нарушения.
type memoryView struct {
	Dir      string        `json:"dir"`
	Exists   bool          `json:"exists"` // папка памяти заведена
	Hot      hotView       `json:"hot"`
	Cold     []noteView    `json:"cold"`
	Queue    []noteView    `json:"queue"`
	Journal  []string      `json:"journal"`
	Problems []problemView `json:"problems"`
	// Decisions — последние решения человека из journal/decisions.md, старые первыми.
	Decisions []string   `json:"decisions"`
	Health    healthView `json:"health"`
}

// State returns the screen; памяти еще нет — пустой вид с Exists=false, папка не создается.
func (d *memoryDesk) State() (memoryView, error) {
	v := memoryView{Dir: d.dir, Cold: []noteView{}, Queue: []noteView{}, Journal: []string{}, Problems: []problemView{}, Decisions: []string{}, Health: newHealthView(domain.Health{IdleDays: -1}, nil, domain.DayOf(time.Now()))}
	if _, err := os.Stat(filepath.Join(d.dir, "HOT.md")); errors.Is(err, os.ErrNotExist) {
		return v, nil
	} else if err != nil {
		return v, err
	}
	svc, _, err := d.service()
	if err != nil {
		return v, err
	}
	ov, err := svc.Overview()
	if err != nil {
		return v, err
	}
	v.Exists = true
	runes := len([]rune(ov.Snapshot.Hot))
	v.Hot = hotView{Text: ov.Snapshot.Hot, Runes: runes, Tokens: runes / 3, Limit: ov.Limits.Hot, Topics: domain.TopicList(ov.Snapshot.Notes)}
	for _, n := range ov.Snapshot.Notes {
		v.Cold = append(v.Cold, noteView{Name: n.Name, Topic: n.Topic, Origin: originOf(n.Origin), Description: n.Description})
	}
	for _, p := range ov.Pending {
		wait := 0
		if !p.Since.IsZero() {
			wait = max(0, int(ov.Today.Sub(domain.DayOf(p.Since)).Hours()/24))
		}
		v.Queue = append(v.Queue, noteView{Name: p.Note.Name, Topic: p.Note.Topic, Origin: originOf(p.Note.Origin), Description: p.Note.Description, Body: p.Body, WaitDays: wait})
	}
	v.Health = newHealthView(ov.Health, ov.Events, ov.Today)
	v.Journal = append(v.Journal, ov.Journal...)
	v.Decisions = append(v.Decisions, ov.Decisions...)
	for _, p := range ov.Problems {
		v.Problems = append(v.Problems, problemView{Kind: string(p.Kind), File: p.File, Detail: p.Detail})
	}
	return v, nil
}

// originOf: запись без поля origin написана человеком.
func originOf(o domain.Origin) string {
	if o == "" {
		return string(domain.OriginHuman)
	}
	return string(o)
}

// Init creates the skeleton; существующую память не трогает.
func (d *memoryDesk) Init() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return memory.New(memorydir.New(d.dir), memory.Limits{}).Init()
}

// guard asks Touch ID before a write that changes HOT.md, which every agent session
// reads (решение владельца 09.11: система доверия). Проверка здесь, в Go, а не в
// окне: страница не может ее пропустить. Отклонение и потолок HOT.md не меняют.
func (d *memoryDesk) guard(reason string) error {
	if d.auth == nil {
		return errors.New("нет помощника kb-auth: Touch ID работает в собранном окне")
	}
	return d.auth.Check(reason)
}

// Approve moves a queued note to the layer the person chose ("cold" или "hot").
// В горячий слой уходит правило, в холодный запись и ее тема в перечне HOT.md:
// и то и другое по Touch ID.
func (d *memoryDesk) Approve(name, to string) error {
	layer, err := domain.ParseLayer(to)
	if err != nil {
		return err
	}
	reason := "добавить запись в холодную память"
	if layer == domain.LayerHot {
		reason = "добавить правило в горячий слой памяти"
	}
	if err := d.guard(reason); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return err
	}
	return svc.Approve(name, layer)
}

// Reject archives a queued note to the journal instead of deleting it.
func (d *memoryDesk) Reject(name string) error {
	// Отклонение считается осмотром очереди и поднимает здоровье, поэтому по Touch ID.
	if err := d.guard("отклонить предложение агента «" + name + "»"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return err
	}
	return svc.Reject(name)
}

// SetHotLimit saves the ceiling of the hot layer; потолок индекса остается как был.
func (d *memoryDesk) SetHotLimit(n int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := memorydir.New(d.dir)
	lim, err := st.Limits()
	if err != nil {
		return err
	}
	lim.Hot = n
	return st.SaveLimits(lim)
}

// Topics rebuilds the topic list of the hot layer from the notes.
func (d *memoryDesk) Topics() (bool, error) {
	if err := d.guard("обновить перечень тем в HOT.md"); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return false, err
	}
	return svc.Topics()
}

// Who rewrites the «Кто я» block from the portrait.
func (d *memoryDesk) Who(src memory.PortraitSource) (bool, error) {
	if err := d.guard("обновить блок «Кто я» в HOT.md"); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return false, err
	}
	return svc.WithPortrait(src).Who()
}

// portraitText is a memory.PortraitSource over a function.
type portraitText func() (string, error)

func (f portraitText) Text() (string, error) { return f() }

// Seal accepts the current HOT.md after the person read it: файл изменен не окном и
// не kbengine (агентом с доступом к файлам или вручную). По Touch ID, подтверждение
// остается в журнале решений.
func (d *memoryDesk) Seal() error {
	if err := d.guard("подтвердить текущий HOT.md"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return err
	}
	return svc.Seal()
}

// Review records that the person looked through the memory and changed nothing.
// По Touch ID: осмотр поднимает доверие, и отметить его должен человек, а не
// страница, которой что-то подсунули.
func (d *memoryDesk) Review() error {
	if err := d.guard("отметить осмотр памяти"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return err
	}
	return svc.Review()
}

// ConfirmRule records that the person re-read a rule of the hot layer and keeps it.
func (d *memoryDesk) ConfirmRule(text string) error {
	if err := d.guard("подтвердить правило: «" + clip(text, 80) + "»"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return err
	}
	return svc.ConfirmRule(text)
}

// ConfirmRules records that the person re-read several rules of the hot layer and
// keeps them: один Touch ID на весь список, запись по каждому правилу, всё или ничего.
func (d *memoryDesk) ConfirmRules(texts []string) error {
	if err := d.guard(fmt.Sprintf("подтвердить правил в памяти: %d", len(texts))); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return err
	}
	return svc.ConfirmRules(texts)
}

// indexReport is the outcome of a vector rebuild as the screen shows it.
type indexReport struct {
	Embedded int `json:"embedded"`
	Reused   int `json:"reused"`
	Failed   int `json:"failed"`
}

// Index rebuilds the vector index of the cold layer. Touch ID не нужен: HOT.md не
// меняется, векторы — производный файл, а решение остается в журнале как обслуживание.
func (d *memoryDesk) Index() (indexReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	svc, _, err := d.service()
	if err != nil {
		return indexReport{}, err
	}
	emb := d.embed
	if emb == nil {
		emb = embedhttp.New(memorydir.EmbedURL, memorydir.EmbedModel)
	}
	rep, err := svc.WithEmbedder(emb, memorydir.EmbedModel).BuildIndex()
	return indexReport{rep.Embedded, rep.Reused, rep.Failed}, err
}

// clip cuts text to n runes for the Touch ID prompt: длинное правило не растягивает диалог.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
