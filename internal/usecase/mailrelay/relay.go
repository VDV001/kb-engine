// Package mailrelay answers the phone from the desk (#622, этап 3): окно
// забирает новое из ящика kb-сервера, открывает вопросы телефона, ведет ход
// модели в своем чате и кладет зашифрованный ответ в тот же чат ящика.
//
// Внутри шифра — Payload в JSON: {"kind": "...", "text": "...", ...}.
// Телефон пишет question (вопрос модели), search (поиск по источникам окна) или
// open (текст найденного по ref); окно отвечает answer (с sources — откуда
// ответ), results (hits), doc (title и text) или error. Поиск и открытие идут
// мимо модели — бесплатны и не тратят ее ход.
//
// Порядок ради одного правила — модель не запускается дважды на один вопрос
// (каждый ход платный): номер сообщения сохраняется до хода модели, ответ —
// сначала в исходящие, потом в ящик. Сбой посреди хода теряет этот вопрос
// (телефон спросит снова), сбой отправки — нет: исходящие уходят следующим
// кругом тем же счетчиком.
//
// ponytail: состояние локальное — правило «один вопрос — один ход» держится
// для одного компьютера на ящик. Два окна с одним токеном и ключом ответят
// оба. Путь апгрейда — метка «взято в работу» в ящике (условная запись) или
// выбор отвечающего компьютера в окне.
package mailrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
)

// Message is one sealed message of the mailbox.
type Message struct {
	Seq    int64
	Chat   string
	From   domain.MailSide
	At     time.Time // время ящика: старше начала состояния — не вопрос
	Sealed []byte
}

// ErrRefused marks a post the server refused for good (велико, неверный чат):
// такой ответ уходит из исходящих, а не затыкает их.
var ErrRefused = errors.New("mailrelay: refused by the server")

// Page is a page of the mailbox after a number; More — за ней есть еще.
type Page struct {
	Messages []Message
	More     bool
}

// Opened is a message that passed the check: кто запечатал и с каким счетчиком.
type Opened struct {
	Sender  string
	Counter uint64
	Plain   []byte
}

// Payload is the text inside the seal. Title, Hits и Sources — у ответов
// окна: заголовок открытого документа, выдача поиска, откуда ответ модели.
type Payload struct {
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Title   string   `json:"title,omitempty"`
	Hits    []Hit    `json:"hits,omitempty"`
	Sources []Source `json:"sources,omitempty"`
}

// Kinds of payload: телефон спрашивает question, ищет search, открывает
// найденное open; окно отвечает answer, results, doc или error.
const (
	KindQuestion = "question"
	KindAnswer   = "answer"
	KindError    = "error"
	KindSearch   = "search"
	KindResults  = "results"
	KindOpen     = "open"
	KindDoc      = "doc"
)

// Hit is one found document: Ref — чем телефон его откроет; Text — только у
// открытого.
type Hit struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind,omitempty"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Where   string `json:"where,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Source is where an answer came from — метка «взято» под ответом.
type Source struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Answer is a model turn with its sources.
type Answer struct {
	Chat, Text string
	Sources    []Source
}

// SourcedAsker is an Asker that also tells where the answer came from.
type SourcedAsker interface {
	AskSourced(ctx context.Context, chat, question string) (Answer, error)
}

// Finder searches the person's sources and opens a found document as text —
// для поиска с телефона. Пароли «Доступов» в нем не участвуют.
type Finder interface {
	Search(ctx context.Context, q string) ([]Hit, error)
	Open(ctx context.Context, ref string) (Hit, error)
}

// Box is the person's mailbox on the kb server.
type Box interface {
	Since(ctx context.Context, after int64) (Page, error)
	Post(ctx context.Context, chat string, sealed []byte) (int64, error)
}

// Crypto opens the phone's messages and seals the desk's: ключ и отправитель
// окна живут в адаптере.
type Crypto interface {
	Open(chat string, from domain.MailSide, sealed []byte) (Opened, error)
	Seal(chat string, counter uint64, plain []byte) ([]byte, error)
}

// Asker runs a model turn in the window's chat ("" — новый) and returns the
// chat id and the answer.
type Asker interface {
	Ask(ctx context.Context, chat, question string) (string, string, error)
}

// Outgoing is a reply waiting to be posted, с уже выданным счетчиком.
type Outgoing struct {
	Chat    string  `json:"chat"`
	Counter uint64  `json:"counter"`
	Payload Payload `json:"payload"`
	// Sealed — запечатано один раз: повтор отправки — те же байты, и сервер
	// склеит строку, дошедшую при потерянном ответе (ревью #635).
	Sealed []byte `json:"sealed"`
}

// State is what the relay remembers between ticks.
type State struct {
	After       int64             `json:"after"`        // последний разобранный номер ящика
	DeskCounter uint64            `json:"desk_counter"` // последний счетчик окна
	Seen        map[string]uint64 `json:"seen"`         // чат|сторона|отправитель → последний счетчик
	Chats       map[string]string `json:"chats"`        // чат ящика → чат окна
	Outbox      []Outgoing        `json:"outbox"`
	KeyID       string            `json:"key_id"` // ключ, под который заведено состояние
	Start       time.Time         `json:"start"`  // вопросы старше — не отвечаются
}

// Store keeps the state.
type Store interface {
	Load() (State, error)
	Save(State) error
}

// Relay answers the phone.
type Relay struct {
	Box    Box
	Crypto Crypto
	Asker  Asker
	Store  Store
	// KeyID — текущий ключ чата: другой, чем у состояния, — состояние заново.
	KeyID string
	// Now — часы начала нового состояния; nil — time.Now.
	Now func() time.Time
	// MaxText — предел текста ответа в байтах (0 — без предела).
	MaxText int
	// Finder — поиск с телефона; nil — телефон получает отказ словами.
	Finder Finder
}

// Tick posts what is waiting, then reads the mailbox to the end and answers
// every new question.
func (r Relay) Tick(ctx context.Context) error {
	st, err := r.load()
	if err != nil {
		return err
	}
	if err := r.flush(ctx, &st); err != nil {
		return err
	}
	for {
		p, err := r.Box.Since(ctx, st.After)
		if err != nil {
			return fmt.Errorf("mailrelay: read: %w", err)
		}
		posted := false
		for _, m := range p.Messages {
			sent, err := r.handle(ctx, &st, m)
			if err != nil {
				return err
			}
			posted = posted || sent
		}
		if err := r.Store.Save(st); err != nil { // одна запись на страницу без вопросов
			return fmt.Errorf("mailrelay: state: %w", err)
		}
		// Дочитать, если есть еще или окно само положило ответы: свои ответы
		// сдвигают номер сразу, а не лишним запросом через круг. Пустой
		// дочиток не нужен — опрос и так на пределе частоты ящика.
		if len(p.Messages) == 0 || (!p.More && !posted) {
			return nil
		}
	}
}

// startSlack — запас на расхождение часов окна и сервера: вопрос, заданный
// сразу после привязки, не должен оказаться «старше начала».
const startSlack = 2 * time.Minute

func (r Relay) load() (State, error) {
	st, err := r.Store.Load()
	if err != nil {
		return State{}, fmt.Errorf("mailrelay: state: %w", err)
	}
	if st.KeyID != r.KeyID {
		// Новый ключ (первая привязка, отвязаны все, другой сервер под тем же id):
		// номер ящика и счетчики — заново, вопросы до начала не отвечаются —
		// иначе платный ход на каждый старый вопрос журнала. Счетчик окна
		// растет дальше: телефон не примет его за повтор.
		now := time.Now
		if r.Now != nil {
			now = r.Now
		}
		st = State{KeyID: r.KeyID, DeskCounter: st.DeskCounter, Start: now().Add(-startSlack)}
	}
	if st.Seen == nil {
		st.Seen = map[string]uint64{}
	}
	if st.Chats == nil {
		st.Chats = map[string]string{}
	}
	return st, nil
}

// request returns the phone's request in m, или ok=false: свое, подделка,
// повтор, чужой вид, пустой текст. Счетчик отправителя запоминается здесь же.
func (r Relay) request(st *State, m Message) (Payload, bool) {
	if m.From != domain.MailPhone || m.At.Before(st.Start) {
		return Payload{}, false
	}
	o, err := r.Crypto.Open(m.Chat, m.From, m.Sealed)
	if err != nil {
		return Payload{}, false // подделка или чужой ключ: не запрос
	}
	key := m.Chat + "|" + string(m.From) + "|" + o.Sender
	if o.Counter <= st.Seen[key] {
		return Payload{}, false // повтор или старое: сервер мог повторить
	}
	st.Seen[key] = o.Counter
	var p Payload
	if json.Unmarshal(o.Plain, &p) != nil || strings.TrimSpace(p.Text) == "" {
		return Payload{}, false
	}
	switch p.Kind {
	case KindQuestion, KindSearch, KindOpen:
		return p, true
	}
	return Payload{}, false
}

// handle answers m if it is a request; sent — ответ ушел в ящик.
func (r Relay) handle(ctx context.Context, st *State, m Message) (bool, error) {
	st.After = max(st.After, m.Seq)
	req, ok := r.request(st, m)
	if !ok {
		return false, nil // запишется концом страницы
	}
	// Номер — до хода модели: сбой посреди хода не запустит ее второй раз.
	if err := r.Store.Save(*st); err != nil {
		return false, fmt.Errorf("mailrelay: state: %w", err)
	}
	var reply Payload
	switch req.Kind {
	case KindQuestion:
		reply = r.ask(ctx, st, m.Chat, req.Text)
	default:
		reply = r.find(ctx, req)
	}
	reply.Text = trim(reply.Text, r.MaxText)
	st.DeskCounter++
	out := Outgoing{Chat: m.Chat, Counter: st.DeskCounter, Payload: reply}
	var err error
	if out.Sealed, err = r.seal(out); err != nil {
		return false, err
	}
	st.Outbox = append(st.Outbox, out)
	if err := r.Store.Save(*st); err != nil {
		return false, fmt.Errorf("mailrelay: state: %w", err)
	}
	return true, r.flush(ctx, st)
}

// ask runs the model turn in the window's chat of this mailbox chat.
func (r Relay) ask(ctx context.Context, st *State, chat, question string) Payload {
	var a Answer
	var err error
	if sa, ok := r.Asker.(SourcedAsker); ok {
		a, err = sa.AskSourced(ctx, st.Chats[chat], question)
	} else {
		a.Chat, a.Text, err = r.Asker.Ask(ctx, st.Chats[chat], question)
	}
	if err != nil {
		return Payload{Kind: KindError, Text: err.Error()}
	}
	st.Chats[chat] = a.Chat
	return Payload{Kind: KindAnswer, Text: a.Text, Sources: a.Sources}
}

// errNoFinder — окно собрано без поиска для телефона.
var errNoFinder = errors.New("поиск с телефона в этом окне не подключен")

// find runs search or open in the window's sources: модель не участвует.
func (r Relay) find(ctx context.Context, req Payload) Payload {
	if r.Finder == nil {
		return Payload{Kind: KindError, Text: errNoFinder.Error()}
	}
	if req.Kind == KindSearch {
		hits, err := r.Finder.Search(ctx, req.Text)
		if err != nil {
			return Payload{Kind: KindError, Text: err.Error()}
		}
		return Payload{Kind: KindResults, Text: req.Text, Hits: hits}
	}
	h, err := r.Finder.Open(ctx, req.Text)
	if err != nil {
		return Payload{Kind: KindError, Text: err.Error()}
	}
	return Payload{Kind: KindDoc, Title: h.Title, Text: h.Text, Hits: []Hit{{Ref: h.Ref, Kind: h.Kind, Title: h.Title, Where: h.Where}}}
}

// trimNote ends an answer cut to the mailbox limit.
const trimNote = "\n… полностью — в окне DVK"

// trim cuts text to max bytes on a rune boundary, с пометкой (0 — без предела).
func trim(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := limit - len(trimNote)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:max(cut, 0)] + trimNote
}

func (r Relay) seal(o Outgoing) ([]byte, error) {
	plain, err := json.Marshal(o.Payload)
	if err != nil {
		return nil, fmt.Errorf("mailrelay: reply: %w", err)
	}
	sealed, err := r.Crypto.Seal(o.Chat, o.Counter, plain)
	if err != nil {
		return nil, fmt.Errorf("mailrelay: seal: %w", err)
	}
	return sealed, nil
}

// flush posts the outbox in order; неотправленное остается до следующего
// круга, отказанное насовсем (ErrRefused) — выбрасывается: иначе оно затыкает
// ящик навсегда (ревью #635).
func (r Relay) flush(ctx context.Context, st *State) error {
	for len(st.Outbox) > 0 {
		o := st.Outbox[0]
		if len(o.Sealed) == 0 { // состояние до запечатывания при постановке
			var err error
			if o.Sealed, err = r.seal(o); err != nil {
				return err
			}
		}
		_, err := r.Box.Post(ctx, o.Chat, o.Sealed)
		if err != nil && !errors.Is(err, ErrRefused) {
			return fmt.Errorf("mailrelay: post: %w", err)
		}
		st.Outbox = st.Outbox[1:]
		if err := r.Store.Save(*st); err != nil {
			return fmt.Errorf("mailrelay: state: %w", err)
		}
	}
	return nil
}
