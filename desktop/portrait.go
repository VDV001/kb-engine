package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/portraitfile"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/portrait"
)

// «Что DVK помнит обо мне» (#479, решение владельца 06.11): портрет в папке,
// которую выбрал человек; новое из чатов ждет его одобрения; в чат идет
// только одобренное и только если помещается — иначе человек выбирает,
// целиком или только нужное.

// Сколько знаков портрета влезает в начало чата: маленькая модель на этом
// Маке держит 4–8 тыс. токенов на все, облачная — с запасом.
// ponytail: два порога вместо размера контекста модели. Потолок: облачная
// модель с маленьким контекстом. Путь: порог от контекста модели в настройках.
const (
	localPortraitBudget = 1500
	cloudPortraitBudget = 4000
)

// errNoPortraitDir — папка портрета не выбрана.
var errNoPortraitDir = errors.New("выберите папку портрета в «Чат и модель → Что DVK помнит обо мне»")

// extractTimeout — потолок разбора одного ответа: он идет фоном и не держит чат.
const extractTimeout = 90 * time.Second

// factView is a fact for the page.
type factView struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Title   string `json:"title"` // раздел для человека
	Text    string `json:"text"`
	Source  string `json:"source"`
	Pending bool   `json:"pending"` // ждет одобрения
}

type sectionView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// portraitView is the portrait for the page: папка, факты, размер и порог.
type portraitView struct {
	Dir      string        `json:"dir"`
	Facts    []factView    `json:"facts"`
	Sections []sectionView `json:"sections"`
	Size     int           `json:"size"`   // знаков одобренного
	Budget   int           `json:"budget"` // сколько влезает в начало чата
}

// portraitNote says to the page whether the portrait went to the chat.
type portraitNote struct {
	Size   int    `json:"size"`
	Budget int    `json:"budget"`
	Over   bool   `json:"over"` // не поместился и не пошел: человек выбирает
	Used   string `json:"used"` // "", full, relevant
}

// proposalView is one fact of a compressed portrait.
type proposalView struct {
	Section string `json:"section"`
	Title   string `json:"title"`
	Text    string `json:"text"`
}

func viewFacts(facts []domain.Fact) []factView {
	out := make([]factView, 0, len(facts))
	for _, f := range facts {
		out = append(out, factView{f.ID(), string(f.Section()), f.Section().Title(), f.Text(), f.Source(), !f.Approved()})
	}
	return out
}

func portraitBudget(c desktopconfig.Chat) int {
	if c.Local() {
		return localPortraitBudget
	}
	return cloudPortraitBudget
}

// portraitAsker asks the chat model one short question for the portrait.
type portraitAsker struct{ m chat.Model }

func (p portraitAsker) Complete(ctx context.Context, system, user string) (string, error) {
	r, err := p.m.Complete(ctx, []chat.ModelMessage{{Role: chat.RoleSystem, Content: system}, {Role: chat.RoleUser, Content: user}}, nil)
	return r.Content, err
}

// portrait returns the portrait service over the chosen folder; model nil —
// для правок вручную модель не нужна.
func (d *chatDesk) portrait(c desktopconfig.Chat, model chat.Model) (*portrait.Service, error) {
	if c.MemoryDir == "" {
		return nil, errNoPortraitDir
	}
	var m portrait.Model
	if model != nil {
		m = portraitAsker{model}
	}
	return portrait.NewService(portraitfile.Store{Dir: c.MemoryDir}, m, d.now, newChatID), nil
}

// withPortrait runs fn under the portrait lock: разбор фоном и правка
// человека не затирают друг друга.
// ponytail: замок на весь разбор, вместе с запросом к модели. Потолок: правка
// ждет до extractTimeout, пока модель думает. Путь: модель вне замка, слияние при записи.
func (d *chatDesk) withPortrait(model chat.Model, fn func(s *portrait.Service) error) error {
	s, err := d.portrait(d.config(), model)
	if err != nil {
		return err
	}
	d.portraitMu.Lock()
	defer d.portraitMu.Unlock()
	return fn(s)
}

// Portrait returns the portrait for the page; папки нет — пустой вид без ошибки.
func (d *chatDesk) Portrait() (portraitView, error) {
	c := d.config()
	v := portraitView{Dir: c.MemoryDir, Facts: []factView{}, Budget: portraitBudget(c)}
	for _, s := range domain.FactSections() {
		v.Sections = append(v.Sections, sectionView{string(s), s.Title()})
	}
	if c.MemoryDir == "" {
		return v, nil
	}
	err := d.withPortrait(nil, func(s *portrait.Service) error {
		p, err := s.Portrait(context.Background())
		if err != nil {
			return err
		}
		v.Facts = viewFacts(p.Facts())
		v.Size = len([]rune(domain.PortraitText(p.Approved())))
		return nil
	})
	return v, portraitErr(err)
}

// PortraitAdd adds a fact typed by the person.
func (d *chatDesk) PortraitAdd(section, text string) error {
	sec, err := domain.NewFactSection(section)
	if err != nil {
		return portraitErr(err)
	}
	return portraitErr(d.withPortrait(nil, func(s *portrait.Service) error {
		_, err := s.Add(context.Background(), sec, text)
		return err
	}))
}

// PortraitEdit changes a fact text.
func (d *chatDesk) PortraitEdit(id, text string) error {
	return portraitErr(d.withPortrait(nil, func(s *portrait.Service) error { return s.Edit(context.Background(), id, text) }))
}

// PortraitRemove forgets a fact (и отклоняет ждущий одобрения).
func (d *chatDesk) PortraitRemove(id string) error {
	return portraitErr(d.withPortrait(nil, func(s *portrait.Service) error { return s.Remove(context.Background(), id) }))
}

// PortraitApprove lets a fact into the chat; text — правка перед одобрением.
func (d *chatDesk) PortraitApprove(id, text string) error {
	return portraitErr(d.withPortrait(nil, func(s *portrait.Service) error { return s.Approve(context.Background(), id, text) }))
}

// PortraitCompress asks the chat model for a shorter portrait; применяется по согласию.
func (d *chatDesk) PortraitCompress() ([]proposalView, error) {
	c := d.config()
	key, err := d.modelKey(c)
	if err != nil {
		return nil, err
	}
	var out []proposalView
	err = d.withPortrait(d.model(c, key), func(s *portrait.Service) error {
		ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
		defer cancel()
		props, err := s.Compress(ctx)
		for _, p := range props {
			out = append(out, proposalView{string(p.Section), p.Section.Title(), p.Text})
		}
		return err
	})
	return out, portraitErr(err)
}

// PortraitApplyCompressed replaces the approved facts with the accepted proposal.
func (d *chatDesk) PortraitApplyCompressed(props []proposalView) error {
	var in []portrait.Proposal
	for _, p := range props {
		sec, err := domain.NewFactSection(p.Section)
		if err != nil {
			return portraitErr(err)
		}
		in = append(in, portrait.Proposal{Section: sec, Text: p.Text})
	}
	return portraitErr(d.withPortrait(nil, func(s *portrait.Service) error { return s.ApplyCompressed(context.Background(), in) }))
}

// portraitFor returns the portrait text for a question and what to tell the page.
func (d *chatDesk) portraitFor(c desktopconfig.Chat, question, mode string) (string, *portraitNote) {
	if c.MemoryDir == "" {
		return "", nil
	}
	var about string
	var note *portraitNote
	err := d.withPortrait(nil, func(s *portrait.Service) error {
		p, err := s.Portrait(context.Background())
		if err != nil {
			return err
		}
		cc := portrait.Context(p, question, portraitBudget(c), portrait.ContextMode(mode))
		about = cc.Text
		note = &portraitNote{Size: cc.Size, Budget: portraitBudget(c), Over: !cc.Fits, Used: mode}
		return nil
	})
	if err != nil {
		log.Printf("портрет: %v", err) // чат отвечает и без портрета
		return "", nil
	}
	return about, note
}

// extractAfter finds new facts in the exchange in the background and tells the page.
func (d *chatDesk) extractAfter(c desktopconfig.Chat, model chat.Model, question, answer string) {
	if c.MemoryDir == "" || c.MemoryOff {
		return
	}
	go func() {
		var found []domain.Fact
		err := d.withPortrait(model, func(s *portrait.Service) error {
			ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
			defer cancel()
			var err error
			found, err = s.Extract(ctx, question, answer)
			return err
		})
		if err != nil {
			log.Printf("портрет: разбор ответа: %v", err)
			return
		}
		if len(found) > 0 && d.memoryNew != nil {
			d.memoryNew(viewFacts(found))
		}
	}()
}

// portraitErr names portrait failures for a person.
func portraitErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errNoPortraitDir):
		return err
	case errors.Is(err, portraitfile.ErrCorrupt):
		return errors.New("файл портрета испорчен — окно его не перезапишет; поправьте или уберите portrait.json в папке портрета")
	case errors.Is(err, domain.ErrFactExists):
		return errors.New("такой факт уже есть")
	case errors.Is(err, domain.ErrFactNotFound):
		return errors.New("этого факта уже нет — портрет изменился")
	case errors.Is(err, domain.ErrInvalidFact):
		return fmt.Errorf("факт не подходит: пустой, длиннее %d знаков или с переводом строки", domain.MaxFactRunes)
	case errors.Is(err, portrait.ErrModelReply):
		return errors.New("модель ответила не по форме — попробуйте еще раз")
	}
	return err
}
