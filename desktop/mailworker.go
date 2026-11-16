package main

// Работник ящика (#622, этап 3): раз в 10 с окно забирает новое из ящика
// каждого kb-сервера, к которому привязаны телефоны (есть ключ чата в Связке),
// и отвечает на вопросы телефона ходом модели — сама логика в
// usecase/mailrelay, здесь адаптеры: ящик, шифр, файл состояния, чат окна.

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// mailEvery — шаг опроса ящика: решение 15.11 (короткий опрос раз в 10 с;
// предел сервера — раз в 5 с).
const mailEvery = 10 * time.Second

// mailWorkerHTTP bounds one mailbox request: зависший запрос не держит круг.
var mailWorkerHTTP = &http.Client{Timeout: 30 * time.Second}

type askFn func(ctx context.Context, chat, question string) (string, string, error)

type mailWorker struct {
	settings *folderSettings
	ask      askFn
	answer   answerFn           // ход модели с метками «взято»; nil — ask без меток
	find     mailrelay.Finder   // поиск с телефона; nil — отказ словами
	dir      string             // где лежат mail-<id>.json
	changed  func(chat string)  // ответ записан в чат окна: странице перечитать список и чат
	turn     func(phoneTurn)    // ход по вопросу с телефона — странице (#646)
	memory   mailrelay.Memory   // решения телефона по «Запомнить о Вас?» (#647); nil — отказ словами
	desk     mailrelay.DeskInfo // модель и источники окна для телефона (#647); nil — отказ словами

	notesMu sync.Mutex
	notes   []mailrelay.Note // «Запомнить о Вас?» для телефонов: пишет разбор ответа, забирает круг
	quiet   func() bool      // «новое — только в портрете»: телефону не предлагать (ревью #655)
	logf    func(string, ...any)
	creds   map[string]mailCreds // только горутина работника
}

// mailCreds is what the worker read from the keychain for a server: ключ чата
// и клиент с токеном. Связка — не каждые 10 с (два вызова security на сервер):
// заново после привязки и отвязки в этом окне (mailGen) и раз в mailCredsTTL.
type mailCreds struct {
	key    mailcrypt.Key
	client kbserver.Client
	none   bool // ключа нет: телефоны не привязаны
	gen    uint64
	at     time.Time
}

const mailCredsTTL = 5 * time.Minute

func newMailWorker(s *folderSettings, ask askFn, dir string) *mailWorker {
	return &mailWorker{settings: s, ask: ask, dir: dir, changed: func(string) {}, turn: func(phoneTurn) {}, logf: log.Printf, creds: map[string]mailCreds{}}
}

// run ticks until ctx ends; круги не перекрываются — одна горутина.
func (w *mailWorker) run(ctx context.Context) {
	t := time.NewTicker(mailEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.tick(ctx); err != nil {
				w.logf("mail: %v", err)
			}
		}
	}
}

// tick answers every server with paired phones; сбой одного не держит другие.
func (w *mailWorker) tick(ctx context.Context) error {
	var errs []error
	// Заметки — один раз за круг и каждому ящику целиком: свои чаты берет
	// каждый, иначе первый сервер съел бы факты чатов второго (#647).
	notes := w.Take()
	for _, id := range w.servers() {
		if err := w.tickServer(ctx, id, notes); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// servers lists the enabled kb servers of the sources.
func (w *mailWorker) servers() []string {
	w.settings.mu.Lock()
	defer w.settings.mu.Unlock()
	var ids []string
	for _, s := range w.settings.cfg.WithSources().Sources {
		if s.Kind == desktopconfig.KindServer && s.Enabled {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// credsFor returns the server's key and client, из памяти, пока Связка не менялась.
func (w *mailWorker) credsFor(id string) (mailCreds, error) {
	gen := w.settings.mailGen.Load()
	if c, ok := w.creds[id]; ok && c.gen == gen && time.Since(c.at) < mailCredsTTL {
		return c, nil
	}
	c := mailCreds{gen: gen, at: time.Now()}
	stored, err := w.settings.tokens.Get(mailKeyAccount(id))
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		c.none = true // телефоны не привязаны: сервер не опрашивается
		w.creds[id] = c
		return c, nil
	case err != nil:
		return c, fmt.Errorf("связка ключей: %w", err)
	}
	if c.key, err = mailcrypt.ParseKey(stored); err != nil {
		return c, fmt.Errorf("ключ чата испорчен: %w", err)
	}
	if c.client, err = w.settings.mailClient(id); err != nil {
		return c, err
	}
	if c.client.HTTP == nil {
		c.client.HTTP = mailWorkerHTTP
	}
	w.creds[id] = c
	return c, nil
}

// mailMaxText — предел текста ответа: JSON-обертка и экранирование (кавычки,
// переводы строк — до 2 байт на знак) не выводят шифротекст за предел ящика.
const mailMaxText = (mailcrypt.MaxPlain - 64) / 2

func (w *mailWorker) tickServer(ctx context.Context, id string, notes []mailrelay.Note) error {
	c, err := w.credsFor(id)
	if err != nil || c.none {
		return err
	}
	file := &mailStateFile{path: filepath.Join(w.dir, "mail-"+url.PathEscape(id)+".json")}
	sender, err := file.sender()
	if err != nil {
		return err
	}
	keyID := c.key.ID()
	relay := mailrelay.Relay{
		Box:     mailBox{c.client},
		Crypto:  mailSeal{key: c.key, sender: sender},
		Asker:   workerAsker{w},
		Finder:  w.finder(),
		Store:   file,
		KeyID:   hex.EncodeToString(keyID[:]),
		MaxText: mailMaxText,
		// Шаги хода — не чаще раза в 3 с: телефон опрашивает раз в 10 с, а
		// предел отправки окна — раз в секунду.
		StepEvery: 3 * time.Second,
		Logf:      w.logf,
		Notifier:  mailNotify{c.client},
		Notes:     notes,
		Memory:    w.memory,
		Desk:      w.desk,

		FindTimeout: phoneFindTimeout,
	}
	return relay.Tick(ctx)
}

// useChat wires the worker to the window's chat: ход модели, поиск,
// «новое — только в портрете» и состояние окна для телефона. Здесь, а не в
// app.go: сборка проверяется тестом.
func (w *mailWorker) useChat(d *chatDesk) {
	w.answer = chatAnswerer(d)
	w.find = newPhoneFinder(d.find, d.openFound) // ставит useSources в main до старта окна
	w.quiet = func() bool { return d.config().MemoryQuiet }
	w.desk = phoneDesk{settings: d.settings}
}

// phoneDesk tells the phone the window's state (#647): модель настроек — для
// чата без ответа, и названия источников — для галочек чата. Телефон только
// показывает, менять — в окне (решение 16.11): выбор с двух устройств требует
// явных прав.
type phoneDesk struct{ settings *folderSettings }

// State lists the enabled sources and the connections the person let the chat
// ask («Чату: разрешить»): закрытое телефон не увидит.
func (d phoneDesk) State(context.Context) (mailrelay.DeskState, error) {
	d.settings.mu.Lock()
	cfg := d.settings.cfg
	d.settings.mu.Unlock()
	st := mailrelay.DeskState{Model: cfg.Chat.WithDefaults().Model, Sources: []mailrelay.DeskSource{}}
	for _, s := range cfg.WithSources().Sources {
		if s.Enabled {
			st.Sources = append(st.Sources, mailrelay.DeskSource{ID: s.ID, Name: sourceLabel(s)})
		}
	}
	for _, c := range cfg.Connections {
		if name, ok := chatConnKinds[c.Kind]; ok && c.Enabled && c.Share {
			st.Sources = append(st.Sources, mailrelay.DeskSource{ID: c.ID, Name: name + " · " + cmp.Or(c.Name, name), Conn: true})
		}
	}
	return st, nil
}

// chatConnKinds are the connections a chat can ask (CHAT_CONN в frontend/app.js).
var chatConnKinds = map[desktopconfig.ConnKind]string{desktopconfig.ConnOzon: "Ozon", desktopconfig.ConnMPStats: "MPStats"}

// phoneFindTimeout bounds one search or open for the phone: работник один на
// все серверы, подвисший источник или огромный PDF не держат его круг.
const phoneFindTimeout = time.Minute

// finder is the phone's search, или отказ словами, если окно собрано без него.
func (w *mailWorker) finder() mailrelay.Finder {
	if w.find == nil {
		return phoneSearchOff{}
	}
	return w.find
}

func (w *mailWorker) askAndTell(ctx context.Context, chat, question string) (string, string, error) {
	id, answer, err := w.ask(ctx, chat, question)
	if err == nil {
		w.changed(id)
	}
	return id, answer, err
}

// remember queues the facts found in a chat's answer for the phone (#647).
// maxPhoneFacts bounds one offer to the phone (ревью #655).
const maxPhoneFacts = 10

func (w *mailWorker) remember(n portraitNews) {
	if len(n.Facts) == 0 || w.quiet != nil && w.quiet() {
		return
	}
	facts := make([]mailrelay.Fact, 0, min(len(n.Facts), maxPhoneFacts))
	for _, f := range n.Facts[:min(len(n.Facts), maxPhoneFacts)] {
		facts = append(facts, mailrelay.Fact{ID: f.ID, Title: f.Title, Text: f.Text})
	}
	w.notesMu.Lock()
	defer w.notesMu.Unlock()
	w.notes = append(w.notes, mailrelay.Note{Chat: n.Chat, Payload: mailrelay.Payload{Kind: mailrelay.KindRemember, Facts: facts}})
}

// Take gives what the window queued for the phones and empties the queue.
func (w *mailWorker) Take() []mailrelay.Note {
	w.notesMu.Lock()
	defer w.notesMu.Unlock()
	q := w.notes
	w.notes = nil
	return q
}

// phoneMemory decides a fact from the phone with the window's portrait (#647):
// «Да» — одобрить с текстом, «Нет» — убрать; decided — странице снять свою карточку.
type phoneMemory struct {
	decide  func(ctx context.Context, id string, yes bool, text string) error
	decided func(id string, yes bool)
}

func (m phoneMemory) Decide(ctx context.Context, id string, yes bool, text string) error {
	if err := m.decide(ctx, id, yes, text); err != nil {
		return err // карточка окна остается: решение не состоялось
	}
	m.decided(id, yes)
	return nil
}

// answerFn runs a model turn and tells where the answer came from; step — ход
// модели по мере ответа (nil — без хода).
// began — чат окна, в котором пойдет ход, известен (до ответа модели, #646).
type answerFn func(ctx context.Context, chat, question string, began func(id string), step func(chat.Use)) (mailrelay.Answer, error)

// phoneTurn is what the window page hears about a phone's question (#646):
// начало хода, шаг или конец; Chat — чат окна.
type phoneTurn struct {
	Chat     string    `json:"chat"`
	Question string    `json:"question,omitempty"`
	Step     *chat.Use `json:"step,omitempty"`
	Done     bool      `json:"done,omitempty"`
}

// workerAsker is the relay's model: с метками «взято», когда окно их знает.
type workerAsker struct{ w *mailWorker }

func (a workerAsker) Ask(ctx context.Context, chat, question string) (string, string, error) {
	return a.w.askAndTell(ctx, chat, question)
}

// AskStepped is AskSourced with the turn's steps for the phone (#622); step
// nil — без хода.
func (a workerAsker) AskStepped(ctx context.Context, id, question string, step func(mailrelay.Step)) (mailrelay.Answer, error) {
	if a.w.answer == nil {
		c, text, err := a.Ask(ctx, id, question)
		return mailrelay.Answer{Chat: c, Text: text}, err
	}
	// Странице — ход по вопросу с телефона (#646): начало с чатом окна, шаги и
	// конец, и при отказе модели — иначе «отвечает» в окне висело бы.
	turnChat := ""
	defer func() {
		if turnChat != "" {
			a.w.turn(phoneTurn{Chat: turnChat, Done: true})
		}
	}()
	began := func(c string) {
		turnChat = c
		a.w.turn(phoneTurn{Chat: c, Question: question})
	}
	each := func(u chat.Use) {
		if turnChat != "" {
			a.w.turn(phoneTurn{Chat: turnChat, Step: &u})
		}
		if step != nil {
			step(mailrelay.Step{Text: stepLabel(u)})
		}
	}
	ans, err := a.w.answer(ctx, id, question, began, each)
	if err == nil {
		a.w.changed(ans.Chat)
	}
	return ans, err
}

func (a workerAsker) AskSourced(ctx context.Context, id, question string) (mailrelay.Answer, error) {
	return a.AskStepped(ctx, id, question, nil)
}

// stepLabel words a step as the window shows it (useName в frontend/app.js):
// «Документы: …», «Интернет · … · вне Ваших источников», имя подключения.
func stepLabel(u chat.Use) string {
	name := u.Name
	switch u.Kind {
	case chat.UseDocs:
		name = "Документы"
	case chat.UseWeb:
		name = "Интернет · " + u.Name
	}
	if u.What != "" {
		name += ": " + u.What
	}
	if u.Kind == chat.UseWeb {
		name += " · вне Ваших источников"
	}
	return name
}

// chatAsker runs a quiet turn in the window's chat: без кусков на страницу —
// у человека может быть открыт другой чат. Чат окна удалили — новый: отказ
// чтения случается до хода модели, повтор не платный.
//
// ponytail: ход идет под общим замком чатов окна — пока модель отвечает
// телефону (до askTimeout), вопрос человека в окне ждет со своим «думаю».
// Путь апгрейда — замок на чат, а не на все чаты.
func chatAsker(d *chatDesk) askFn {
	answer := chatAnswerer(d)
	return func(ctx context.Context, chat, question string) (string, string, error) {
		a, err := answer(ctx, chat, question, nil, nil)
		return a.Chat, a.Text, err
	}
}

// chatAnswerer is chatAsker with the «взято» labels of the answer (answerSources).
func chatAnswerer(d *chatDesk) answerFn {
	return func(ctx context.Context, chat, question string, began func(string), step func(chat.Use)) (mailrelay.Answer, error) {
		quiet := askOut{worker: true, step: step, began: began}
		rec, err := d.ask(ctx, askReq{ID: chat, Question: question}, quiet)
		if chat != "" && errors.Is(err, fs.ErrNotExist) {
			rec, err = d.ask(ctx, askReq{Question: question}, quiet)
		}
		if err != nil {
			return mailrelay.Answer{}, err
		}
		// Модель и источники чата — телефону для показа (#647); источники пусто — все.
		a := mailrelay.Answer{Chat: rec.ID, Sources: answerSources(rec), Model: rec.Model, Only: rec.Sources}
		a.Context = (*mailrelay.ContextFill)(rec.Context) // круг контекста на телефоне (#647); та же форма
		if n := len(rec.Messages); n > 0 {
			a.Text = rec.Messages[n-1].Text
		}
		return a, nil
	}
}

// mailBox is the kb server's mailbox for the relay.
// mailNotify asks the kb-server to push the phones after an answer.
type mailNotify struct{ c kbserver.Client }

func (n mailNotify) Notify(ctx context.Context) error {
	_, err := n.c.Notify(ctx)
	return err
}

type mailBox struct{ c kbserver.Client }

func (b mailBox) Since(ctx context.Context, after int64) (mailrelay.Page, error) {
	p, err := b.c.MailSince(ctx, after)
	if err != nil {
		return mailrelay.Page{}, err
	}
	out := mailrelay.Page{More: p.More}
	for _, m := range p.Messages {
		out.Messages = append(out.Messages, mailrelay.Message{Seq: m.Seq, Chat: m.Chat, From: domain.MailSide(m.From), At: m.At, Sealed: m.Sealed})
	}
	return out, nil
}

// Post marks a refusal for good (неверный чат, велико) as mailrelay.ErrRefused:
// такой ответ уходит из исходящих, а не затыкает ящик (ревью #635).
func (b mailBox) Post(ctx context.Context, chat string, sealed []byte) (int64, error) {
	seq, err := b.c.MailPost(ctx, chat, sealed)
	if errors.Is(err, kbserver.ErrRejected) {
		return 0, fmt.Errorf("%w: %w", mailrelay.ErrRefused, err)
	}
	return seq, err
}

// mailSeal opens with the chat key and seals as the window's sender.
type mailSeal struct {
	key    mailcrypt.Key
	sender mailcrypt.Sender
}

func (s mailSeal) Open(chat string, from domain.MailSide, sealed []byte) (mailrelay.Opened, error) {
	m, plain, err := mailcrypt.Open(s.key, chat, from, sealed)
	if err != nil {
		return mailrelay.Opened{}, err
	}
	return mailrelay.Opened{Sender: hex.EncodeToString(m.Sender[:]), Counter: m.Counter, Plain: plain}, nil
}

func (s mailSeal) Seal(chat string, counter uint64, plain []byte) ([]byte, error) {
	return mailcrypt.Seal(s.key, mailcrypt.Meta{Chat: chat, From: domain.MailDesk, Sender: s.sender, Counter: counter}, plain)
}

// mailStateFile keeps the relay state and the window's sender id per server;
// отправитель читается раз (sender), Save пишет файл, не перечитывая его.
type mailStateFile struct {
	path      string
	senderHex string
}

type mailStateDTO struct {
	Sender string          `json:"sender"` // hex 8 байт: отправитель окна в ящике
	State  mailrelay.State `json:"state"`
}

func (f *mailStateFile) read() (mailStateDTO, error) {
	var d mailStateDTO
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("%s: %w", f.path, err)
	}
	return d, nil
}

func (f *mailStateFile) write(d mailStateDTO) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path) // целиком или никак: номер ящика не теряется наполовину
}

// sender returns the window's sender id, создавая его при первом круге.
func (f *mailStateFile) sender() (mailcrypt.Sender, error) {
	d, err := f.read()
	if err != nil {
		return mailcrypt.Sender{}, err
	}
	var s mailcrypt.Sender
	if b, err := hex.DecodeString(d.Sender); err == nil && len(b) == len(s) {
		f.senderHex = d.Sender
		return mailcrypt.Sender(b), nil
	}
	s = mailcrypt.NewSender()
	f.senderHex = hex.EncodeToString(s[:])
	d.Sender = f.senderHex
	return s, f.write(d)
}

func (f *mailStateFile) Load() (mailrelay.State, error) {
	d, err := f.read()
	return d.State, err
}

func (f *mailStateFile) Save(st mailrelay.State) error {
	return f.write(mailStateDTO{Sender: f.senderHex, State: st})
}
