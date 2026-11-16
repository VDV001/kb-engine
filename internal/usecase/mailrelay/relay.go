// Package mailrelay answers the phone from the desk (#622, этап 3): окно
// забирает новое из ящика kb-сервера, открывает вопросы телефона, ведет ход
// модели в своем чате и кладет зашифрованный ответ в тот же чат ящика.
//
// Внутри шифра — Payload в JSON: {"kind": "...", "text": "...", ...}.
// Телефон пишет question (вопрос модели), search (поиск по источникам окна) или
// open (текст найденного по ref); окно отвечает answer (с sources — откуда
// ответ), results (hits), doc (title и text) или error; пока модель думает —
// accepted (взято в работу) и step (ход): оба — только при StepEvery > 0
// (окно включает всегда), без шагов телефон ждет ответа словами «отправлено». Поиск и открытие идут
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	// Context — заполнение контекста чата после ответа (#647); нет счета — nil.
	Context *ContextFill `json:"context,omitempty"`
	// Facts — «Запомнить о Вас?» (#647): предложение окна или решение телефона.
	Facts []Fact `json:"facts,omitempty"`
}

// ContextFill is how full the chat's context is: токены по счету провайдера и
// размер окна модели (0 — не указан).
type ContextFill struct {
	Used   int `json:"used"`
	Window int `json:"window"`
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
	// KindStep — ход модели: куда чат ходит сейчас (окно → телефон, #622).
	KindStep = "step"
	// KindAccepted — окно взяло вопрос в работу (#646): телефон пишет «компьютер принял».
	KindAccepted = "accepted"
	// KindRemember — окно предлагает запомнить факты о человеке (#647, Facts).
	KindRemember = "remember"
	// KindMemory — решение телефона по факту (Text yes/no, Facts[0]) и ответ окна
	// (Text saved/skipped или слова отказа).
	KindMemory = "memory"
)

// Решения и итоги «Запомнить о Вас?».
const (
	MemoryYes     = "yes"
	MemoryNo      = "no"
	MemorySaved   = "saved"
	MemorySkipped = "skipped"
	// MemoryGone — факт не предлагался этому чату, уже решен или забыт: карточку
	// телефон снимает (ревью #655). Слова рисует телефон: здесь только коды.
	MemoryGone = "gone"
	// MemoryBad — решение не разобрано; MemoryUnsupported — окно не умеет.
	MemoryBad         = "bad"
	MemoryUnsupported = "unsupported"
	// MemoryStale — факт этому чату не предлагался или выпал из предложенного:
	// решить его можно в окне, карточка остается со словами (повторное ревью #655).
	MemoryStale = "stale"
	// MemoryFailed — сбой портрета окна; причина — в журнале окна, не на телефоне.
	MemoryFailed = "failed"
)

// Fact is a fact about the person the window offers to remember.
type Fact struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"` // раздел портрета словами
	Text  string `json:"text,omitempty"`
}

// Note is something the window says to a phone chat by itself, вне ответа на
// запрос: Chat — чат окна, ящик находит его чат по State.Chats.
type Note struct {
	Chat    string
	Payload Payload
}

// Memory decides a fact the window offered: yes — запомнить с текстом text.
type Memory interface {
	Decide(ctx context.Context, id string, yes bool, text string) error
}

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
	Context    *ContextFill
}

// SourcedAsker is an Asker that also tells where the answer came from.
type SourcedAsker interface {
	AskSourced(ctx context.Context, chat, question string) (Answer, error)
}

// Step is one step of the model's turn as the person reads it: «Документы:
// поставки», «Ozon · кабинет: заказы» — подпись строит окно, как у себя.
type Step struct {
	Text string
}

// SteppedAsker is a SourcedAsker that also reports the turn's steps while it
// runs: телефон видит ход, а не только итог (#622).
type SteppedAsker interface {
	AskStepped(ctx context.Context, chat, question string, step func(Step)) (Answer, error)
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
	// Notify — после отправки позвать телефон push: это итог хода модели.
	Notify bool `json:"notify,omitempty"`
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
	// Offered — факты, предложенные телефону: чат ящика → id. Решение телефона
	// принимается только по ним (#647).
	Offered map[string][]string `json:"offered,omitempty"`
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
	// FindTimeout bounds one search or open (0 — без срока): работник ящика
	// один на все серверы, зависший источник не держит его круг.
	FindTimeout time.Duration
	// StepEvery — не чаще стольких шагов хода в ящик (0 — шаги не шлются):
	// телефон опрашивает раз в 10 с, а у окна предел отправки раз в секунду.
	StepEvery time.Duration
	// Logf — журнал сбоев шагов хода (nil — молча).
	Logf func(string, ...any)
	// Notifier зовет телефоны push после итога хода модели (nil — не зовет).
	Notifier Notifier
	// Notes — то, что окно говорит телефону само: «Запомнить о Вас?». Круг берет
	// чаты своего ящика, остальное — мимо.
	Notes []Note
	// Memory — решения телефона по предложенным фактам (nil — отказ словами).
	Memory Memory
}

// Notifier tells the phones an answer is waiting (kb-сервер, /api/mail/notify).
type Notifier interface {
	Notify(ctx context.Context) error
}

// Tick posts what is waiting, then reads the mailbox to the end and answers
// every new question.
func (r Relay) Tick(ctx context.Context) error {
	st, err := r.start(ctx)
	if err != nil {
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

// start loads the state and posts what waits: заметки окна и исходящие.
func (r Relay) start(ctx context.Context) (State, error) {
	st, err := r.load()
	if err != nil {
		return st, err
	}
	if err := r.offer(&st); err != nil {
		return st, err
	}
	return st, r.flush(ctx, &st)
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
		st = State{KeyID: r.KeyID, DeskCounter: st.DeskCounter, Start: r.now().Add(-startSlack)}
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
	case KindQuestion, KindSearch, KindOpen, KindMemory:
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
	case KindMemory:
		reply = r.decide(ctx, st, m.Chat, req)
	default:
		reply = r.find(ctx, req)
	}
	reply.Text = trim(reply.Text, r.MaxText)
	st.DeskCounter++
	out := Outgoing{Chat: m.Chat, Counter: st.DeskCounter, Payload: reply, Notify: req.Kind == KindQuestion}
	var err error
	if out.Sealed, err = r.sealFitting(&out); err != nil {
		return false, err
	}
	st.Outbox = append(st.Outbox, out)
	if err := r.Store.Save(*st); err != nil {
		return false, fmt.Errorf("mailrelay: state: %w", err)
	}
	return true, r.flush(ctx, st)
}

func (r Relay) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// ask runs the model turn in the window's chat of this mailbox chat.
func (r Relay) ask(ctx context.Context, st *State, chat, question string) Payload {
	var a Answer
	var err error
	if sa, ok := r.Asker.(SteppedAsker); ok && r.StepEvery > 0 {
		step, stop := r.stepper(ctx, st, chat)
		a, err = sa.AskStepped(ctx, st.Chats[chat], question, step)
		stop()
	} else if sa, ok := r.Asker.(SourcedAsker); ok {
		a, err = sa.AskSourced(ctx, st.Chats[chat], question)
	} else {
		a.Chat, a.Text, err = r.Asker.Ask(ctx, st.Chats[chat], question)
	}
	if err != nil {
		return Payload{Kind: KindError, Text: err.Error()}
	}
	st.Chats[chat] = a.Chat
	return Payload{Kind: KindAnswer, Text: a.Text, Sources: a.Sources, Context: a.Context}
}

// minPostGap — предел частоты отправки окна в ящик (сервер: раз в секунду).
const minPostGap = time.Second

// maxOffered bounds the facts remembered as offered per mailbox chat: старые
// без решения выпадают, телефон по ним получит «не предлагался».
const maxOffered = 20

// offer queues the window's notes for their phone chats (#647): чат окна →
// чат ящика по State.Chats; чат, которого телефон не заводил, — мимо.
func (r Relay) offer(st *State) error {
	queued := false
	for _, n := range r.Notes {
		chat := mailChat(st, n.Chat)
		if chat == "" {
			continue
		}
		st.DeskCounter++
		out := Outgoing{Chat: chat, Counter: st.DeskCounter, Payload: n.Payload}
		var err error
		if out.Sealed, err = r.sealFitting(&out); err != nil {
			// Не влезло (shrink фактов не уменьшает) — мимо этой заметки, круг идет
			// дальше: вопросы телефона важнее предложения (ревью #655).
			r.logf("mailrelay: %s: %v", n.Payload.Kind, err)
			continue
		}
		st.Outbox = append(st.Outbox, out)
		if st.Offered == nil {
			st.Offered = map[string][]string{}
		}
		for _, f := range n.Payload.Facts {
			st.Offered[chat] = append(st.Offered[chat], f.ID)
		}
		if o := st.Offered[chat]; len(o) > maxOffered {
			st.Offered[chat] = o[len(o)-maxOffered:]
		}
		queued = true
	}
	if !queued {
		return nil
	}
	if err := r.Store.Save(*st); err != nil {
		return fmt.Errorf("mailrelay: state: %w", err)
	}
	return nil
}

// mailChat is the mailbox chat of window chat w; "" — телефон его не заводил.
func mailChat(st *State, w string) string {
	for mc, wc := range st.Chats {
		if wc == w {
			return mc
		}
	}
	return ""
}

// decide applies the phone's decision on a fact offered to this chat (#647):
// только по предложенному и один раз — иначе телефон по id удалил бы чужой или
// уже одобренный факт портрета.
func (r Relay) decide(ctx context.Context, st *State, chat string, req Payload) Payload {
	reply := Payload{Kind: KindMemory}
	if len(req.Facts) > 0 { // с id — телефон найдет свою карточку и в отказе
		reply.Facts = []Fact{{ID: req.Facts[0].ID}}
	}
	if len(req.Facts) != 1 || (req.Text != MemoryYes && req.Text != MemoryNo) {
		reply.Text = MemoryBad
		return reply
	}
	f := req.Facts[0]
	i := slices.Index(st.Offered[chat], f.ID)
	switch {
	case i < 0:
		reply.Text = MemoryStale
	case r.Memory == nil:
		reply.Text = MemoryUnsupported
	default:
		yes := req.Text == MemoryYes
		err := r.Memory.Decide(ctx, f.ID, yes, f.Text)
		switch {
		case errors.Is(err, domain.ErrFactDecided), errors.Is(err, domain.ErrFactNotFound): // решен или забыт в окне
			reply.Text = MemoryGone
		case err != nil:
			r.logf("mailrelay: memory %s: %v", f.ID, err) // причина — в журнал окна: в ней пути компьютера
			reply.Text = MemoryFailed
			return reply
		default:
			reply.Text = map[bool]string{true: MemorySaved, false: MemorySkipped}[yes]
		}
		st.Offered[chat] = slices.Delete(st.Offered[chat], i, i+1)
	}
	return reply
}

// maxStepText — столько байт подписи шага уходит телефону.
const maxStepText = 200

// stepPostTimeout bounds one step's post: шаг — показ хода, медленный
// kb-сервер не должен держать его дольше.
const stepPostTimeout = 5 * time.Second

// stepper posts the turn's steps from its own goroutine (ревью #641): ход
// модели не ждет ящик, а из шагов внутри StepEvery уходит самый свежий —
// телефон видит то же, что окно. Шаг — показ, а не обещание: мимо исходящих,
// потерялся — итог все равно придет. Счетчик окна записывается до отправки.
// stop ждет горутину: дальше состояние снова только у handle.
//
// ponytail: шаги остаются в журнале ящика на сервере навсегда, как и ответы;
// новый ключ перечитывает их. Потолок — срок хранения ящика на сервере.
func (r Relay) stepper(ctx context.Context, st *State, chat string) (step func(Step), stop func()) {
	in := make(chan Step, 1)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.stepLoop(ctx, in, st, chat)
	}()
	step = func(s Step) {
		for {
			select {
			case in <- s:
				return
			default: // неотправленный вытесняется свежим
				select {
				case <-in:
				default:
				}
			}
		}
	}
	stop = func() {
		cancel()
		<-done
	}
	return step, stop
}

// stepLoop posts steps from in until ctx ends, не чаще StepEvery: после паузы
// уходит самый свежий из пришедших за нее.
func (r Relay) stepLoop(ctx context.Context, in chan Step, st *State, chat string) {
	var last time.Time
	// Первым — «компьютер принял» (#646): телефон видит, что вопрос взят, пока
	// модель еще думает, а не только по первому шагу. Тем же пределом частоты.
	// Первый шаг ждет после него только предел частоты ящика, а не весь StepEvery (ревью #653).
	if began := r.now(); r.post(ctx, st, chat, Payload{Kind: KindAccepted}) {
		last = began.Add(min(minPostGap, r.StepEvery) - r.StepEvery)
	}
	for {
		var s Step
		select {
		case <-ctx.Done():
			return
		case s = <-in:
		}
		if wait := r.StepEvery - r.now().Sub(last); !last.IsZero() && wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			select {
			case s = <-in: // свежий за время ожидания
			default:
			}
		}
		began := r.now()
		if r.post(ctx, st, chat, Payload{Kind: KindStep, Text: clip(s.Text, maxStepText)}) {
			last = began
		}
	}
}

// post seals and posts one show of the turn (шаг, «принял»); false — не ушел
// (причина в журнале).
func (r Relay) post(ctx context.Context, st *State, chat string, p Payload) bool {
	st.DeskCounter++
	out := Outgoing{Chat: chat, Counter: st.DeskCounter, Payload: p}
	if err := r.Store.Save(*st); err != nil {
		r.logf("mailrelay: %s: state: %v", p.Kind, err)
		return false
	}
	sealed, err := r.sealFitting(&out)
	if err != nil {
		r.logf("mailrelay: %s: %v", p.Kind, err)
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, stepPostTimeout)
	defer cancel()
	if _, err := r.Box.Post(ctx, chat, sealed); err != nil {
		if ctx.Err() == nil { // ход кончился раньше отправки — не сбой
			r.logf("mailrelay: %s: post: %v", p.Kind, err)
		}
		return false
	}
	return true
}

func (r Relay) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// clip cuts text to max bytes on a rune boundary with «…»: у шага нет «полностью
// в окне» — это подпись, а не ответ.
func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:max(cut, 0)] + "…"
}

// ErrNoFinder — relay собран без поиска для телефона. Слова для человека
// подставляет окно своим Finder; это — только на случай сборки без него.
var ErrNoFinder = errors.New("mailrelay: no finder")

// find runs search or open in the window's sources: модель не участвует.
func (r Relay) find(ctx context.Context, req Payload) Payload {
	if r.Finder == nil {
		return Payload{Kind: KindError, Text: ErrNoFinder.Error()}
	}
	if r.FindTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.FindTimeout)
		defer cancel()
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

// sealFitting seals o, shrinking the reply until it fits the mailbox: предел
// текста — оценка, а JSON раздувает управляющие знаки до 6 байт, и заголовок,
// выдача и метки в нее не входят (ревью #636). Не влезший ответ пропал бы:
// номер запроса уже записан, второй раз его не обработают.
func (r Relay) sealFitting(o *Outgoing) ([]byte, error) {
	for {
		sealed, err := r.seal(*o)
		if !errors.Is(err, domain.ErrMailTooLarge) || !shrink(&o.Payload) {
			return sealed, err
		}
	}
}

// shrink makes p smaller by one step: текст вдвое, потом выдача вдвое, потом
// без меток; false — уменьшать больше нечего.
func shrink(p *Payload) bool {
	switch {
	case len(p.Text) > len(trimNote):
		p.Text = trim(p.Text, max(len(p.Text)/2, len(trimNote)))
	case len(p.Hits) > 1:
		p.Hits = p.Hits[:len(p.Hits)/2]
	case len(p.Sources) > 0:
		p.Sources = nil
	default:
		return false
	}
	return true
}

func (r Relay) seal(o Outgoing) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // < > & как есть: \u003c втрое длиннее, а страница не HTML
	if err := enc.Encode(o.Payload); err != nil {
		return nil, fmt.Errorf("mailrelay: reply: %w", err)
	}
	plain := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
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
		// После записи состояния: сбой посреди круга лучше потеряет одно
		// уведомление, чем пришлет его дважды.
		if o.Notify && err == nil {
			r.notify(ctx)
		}
	}
	return nil
}

// notifyTimeout bounds the push call: сервер сам укладывается в 5 с.
const notifyTimeout = 10 * time.Second

func (r Relay) notify(ctx context.Context) {
	if r.Notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if err := r.Notifier.Notify(ctx); err != nil {
		r.logf("mailrelay: notify: %v", err)
	}
}
