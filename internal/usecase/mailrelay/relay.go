// Package mailrelay answers the phone from the desk (#622, этап 3): окно
// забирает новое из ящика kb-сервера, открывает вопросы телефона, ведет ход
// модели в своем чате и кладет зашифрованный ответ в тот же чат ящика.
//
// Внутри шифра — Payload в JSON: {"kind": "question" | "answer" | "error",
// "text": "..."}; телефон пишет question, окно — answer или error.
//
// Порядок ради одного правила — модель не запускается дважды на один вопрос
// (каждый ход платный): номер сообщения сохраняется до хода модели, ответ —
// сначала в исходящие, потом в ящик. Сбой посреди хода теряет этот вопрос
// (телефон спросит снова), сбой отправки — нет: исходящие уходят следующим
// кругом тем же счетчиком.
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

// Payload is the text inside the seal.
type Payload struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Kinds of payload.
const (
	KindQuestion = "question"
	KindAnswer   = "answer"
	KindError    = "error"
)

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

// question returns the phone's question in m, или ok=false: свое, подделка,
// повтор, чужой вид. Счетчик отправителя запоминается здесь же.
func (r Relay) question(st *State, m Message) (string, bool) {
	if m.From != domain.MailPhone || m.At.Before(st.Start) {
		return "", false
	}
	o, err := r.Crypto.Open(m.Chat, m.From, m.Sealed)
	if err != nil {
		return "", false // подделка или чужой ключ: не вопрос
	}
	key := m.Chat + "|" + string(m.From) + "|" + o.Sender
	if o.Counter <= st.Seen[key] {
		return "", false // повтор или старое: сервер мог повторить
	}
	st.Seen[key] = o.Counter
	var p Payload
	if json.Unmarshal(o.Plain, &p) != nil || p.Kind != KindQuestion || strings.TrimSpace(p.Text) == "" {
		return "", false
	}
	return p.Text, true
}

// handle answers m if it is a question; sent — ответ ушел в ящик.
func (r Relay) handle(ctx context.Context, st *State, m Message) (bool, error) {
	st.After = max(st.After, m.Seq)
	text, ok := r.question(st, m)
	if !ok {
		return false, nil // запишется концом страницы
	}
	// Номер — до хода модели: сбой посреди хода не запустит ее второй раз.
	if err := r.Store.Save(*st); err != nil {
		return false, fmt.Errorf("mailrelay: state: %w", err)
	}
	chat, answer, err := r.Asker.Ask(ctx, st.Chats[m.Chat], text)
	reply := Payload{Kind: KindAnswer, Text: answer}
	if err != nil {
		reply = Payload{Kind: KindError, Text: err.Error()}
	} else {
		st.Chats[m.Chat] = chat
	}
	reply.Text = trim(reply.Text, r.MaxText)
	st.DeskCounter++
	out := Outgoing{Chat: m.Chat, Counter: st.DeskCounter, Payload: reply}
	if out.Sealed, err = r.seal(out); err != nil {
		return false, err
	}
	st.Outbox = append(st.Outbox, out)
	if err := r.Store.Save(*st); err != nil {
		return false, fmt.Errorf("mailrelay: state: %w", err)
	}
	return true, r.flush(ctx, st)
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
