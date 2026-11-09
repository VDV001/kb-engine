package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// Экран «Память» (ADR-0008): два слоя и журнал рядом, очередь на одобрение,
// потолок горячего слоя. Окно показывает те же файлы, что читает терминал
// (`kbengine memory …`); ни своего хранилища, ни своих правил у него нет.

// memoryDesk is the memory screen over one folder of the person.
type memoryDesk struct {
	dir  string
	auth authHelper // Touch ID для изменений горячего слоя; nil — окно не из сборки, помощника нет
	mu   sync.Mutex // изменения идут по одному: одобрение и потолок читают-пишут один HOT.md
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
	Body        string `json:"body,omitempty"` // только у очереди: решать надо по тексту
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
}

// State returns the screen; памяти еще нет — пустой вид с Exists=false, папка не создается.
func (d *memoryDesk) State() (memoryView, error) {
	v := memoryView{Dir: d.dir, Cold: []noteView{}, Queue: []noteView{}, Journal: []string{}, Problems: []problemView{}}
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
		v.Queue = append(v.Queue, noteView{Name: p.Note.Name, Topic: p.Note.Topic, Origin: originOf(p.Note.Origin), Description: p.Note.Description, Body: p.Body})
	}
	v.Journal = append(v.Journal, ov.Journal...)
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

// Approve moves a queued note to the layer the person chose ("cold" или "hot").
func (d *memoryDesk) Approve(name, to string) error {
	layer, err := domain.ParseLayer(to)
	if err != nil {
		return err
	}
	// Правило в горячем слое начинает действовать во всех сессиях агентов сразу:
	// решение владельца 09.11 — Touch ID каждый раз. Проверка здесь, в Go, а не в
	// окне: страница не может ее пропустить.
	if layer == domain.LayerHot {
		if d.auth == nil {
			return errors.New("нет помощника kb-auth: Touch ID работает в собранном окне")
		}
		if err := d.auth.Check("добавить правило в горячий слой памяти"); err != nil {
			return err
		}
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
