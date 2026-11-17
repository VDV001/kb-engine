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
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	// Only — источники чата окна после хода (#647): телефон их показывает,
	// менять — в окне; пусто — все.
	Only []string `json:"only,omitempty"`
	// Desk — состояние окна для телефона (#647), ответ на desk.
	Desk *DeskState `json:"desk,omitempty"`
	// File — запись телефона в файлах ящика (#648, KindVoice).
	File *FileRef `json:"file,omitempty"`
	// Heard — что окно расслышало в голосовом вопросе (#648, ответ на KindVoice).
	Heard string `json:"heard,omitempty"`
	// Attach — файл к вопросу телефона (#650): куски в файлах ящика по порядку.
	Attach  *Attachment `json:"attach,omitempty"`
	History []Exchange  `json:"history,omitempty"`
	// Pick — выбор телефона для его ходов в чате (#657): в вопросе — если
	// меняли, в ответе — подтвержденный окном.
	Pick *Pick `json:"pick,omitempty"`
}

// Pick is the phone's choice for its turns in a chat (#657).
type Pick struct {
	Model string   `json:"model,omitempty"`
	Only  []string `json:"only,omitempty"`
}

// Picker applies the phone's choice to a window chat.
type Picker interface {
	SetPick(ctx context.Context, chat string, p Pick) error
}

// Attachment is a file the phone attached to a question: имя, тип, длина
// открытого файла и куски по порядку — сообщение аутентично, поэтому сервер
// не переставит, не подменит и не отрежет куски.
type Attachment struct {
	Name   string    `json:"name"`
	Type   string    `json:"type"`
	Size   int       `json:"size"`
	Pieces []FileRef `json:"pieces"`
}

// MaxAttachment bounds one file from the phone (#650, решение владельца 16.11).
const MaxAttachment = 10 << 20

// File is an opened attachment for the model turn.
type File struct {
	Name, Type string
	Data       []byte
}

// ChatEditor renames and deletes the window's chats (#649).
type ChatEditor interface {
	RenameChat(ctx context.Context, id, title string) error
	DeleteChat(ctx context.Context, id string) error
}

// FileOpener decrypts a sealed piece of the mailbox files.
type FileOpener interface {
	OpenFile(sealed []byte) ([]byte, error)
}

// AttachedAsker runs a turn with files attached (#650); как SteppedAsker.
type AttachedAsker interface {
	AskAttached(ctx context.Context, chat, question string, files []File, step func(Step)) (Answer, error)
}

// FileRef names a piece in the mailbox files (#658): id выдал сервер, sha256 —
// запечатанного куска, сообщение аутентично — сервер кусок не подменит.
type FileRef struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

// VoiceFiles reads and removes the phone's recordings in the mailbox files.
type VoiceFiles interface {
	Get(ctx context.Context, id string) ([]byte, error)
	Delete(ctx context.Context, id string) error
}

// Hearer opens a sealed recording and turns it into text.
type Hearer interface {
	Hear(ctx context.Context, sealed []byte) (string, error)
}

// DeskState is what the phone shows by its input: модель настроек окна и
// названия источников и подключений чата.
type DeskState struct {
	Model   string       `json:"model"`
	Sources []DeskSource `json:"sources"`
	// Hear — кто распознает голос телефона (#648): HearDevice, HearApple,
	// HearCloud, HearServer; пусто — голоса нет. Телефон говорит это человеку:
	// звук уходит дальше компьютера всюду, кроме HearDevice.
	Hear string `json:"hear,omitempty"`
}

// Кто распознает голос телефона.
const (
	HearDevice = "device" // Apple Speech на Маке, без сети
	HearApple  = "apple"  // Apple Speech через серверы Apple
	HearCloud  = "cloud"  // облако из «Слушает»
	HearServer = "server" // свой сервер распознавания
)

// DeskSource is a source or a connection the chat can search.
type DeskSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Conn bool   `json:"conn,omitempty"` // подключение (Ozon, MPStats), а не папка или сервер
}

// DeskInfo tells the window's state for the phone.
type DeskInfo interface {
	State(ctx context.Context) (DeskState, error)
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
	// KindDesk — телефон спрашивает состояние окна, окно отвечает им (#647, Desk).
	KindDesk = "desk"
	// DeskUnsupported — код отказа на desk (Text у KindError): окно собрано без
	// Desk. Слова человеку — у телефона.
	DeskUnsupported = "unsupported"
	// KindVoice — голос телефона (#648): File — запись, Text — VoiceDictate или VoiceAsk.
	KindVoice = "voice"
	// KindHeard — ответ на диктовку: Text — расслышанное (пусто — тишина).
	KindHeard = "heard"
	// VoiceDictate — текст в поле ввода; VoiceAsk — вопрос голосом, ответ модели.
	VoiceDictate = "dictate"
	VoiceAsk     = "ask"
	// VoiceMismatch — код отказа: запись не та, что назвало сообщение (хеш).
	VoiceMismatch = "mismatch"
	// VoiceGone — код: запись не забралась (нет, истекла, сбой сети).
	VoiceGone = "gone"
	// VoiceUnheard — код: распознавание не удалось; причина — в журнале окна.
	VoiceUnheard = "unheard"
	// AttachTooLarge — код: файл телефона больше MaxAttachment.
	AttachTooLarge = "toolarge"
	// VoiceSilent — код: в вопросе голосом не расслышано слов (тишина).
	VoiceSilent = "silent"
	// KindRename — чат переименован (#649): телефон → окно и окно → телефон, Text — название.
	KindRename = "rename"
	// KindDelete — чат удален (#649): телефон → окно и окно → телефон.
	KindDelete = "delete"
	// KindEdited — окно приняло переименование или удаление с телефона (Text — вид).
	KindEdited = "edited"
	// ChatEditFailed — код: окно не смогло переименовать или удалить чат.
	ChatEditFailed = "chaterr"
	// KindAsked — вопрос, заданный в окне (#669): Text — вопрос, Title — название чата.
	KindAsked = "asked"
	// KindHistory — история чата окна пачкой (#669): History, Title.
	KindHistory = "history"
	// DeleteEvicted — Text у KindDelete: чат окна выпал из MaxSharedChats.
	DeleteEvicted = "evicted"
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
	// Only — источники чата после хода (#647); пусто — все.
	Only []string
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
	// Titles — название с телефона для чата, которого окно еще не знает: ляжет
	// на чат окна после первого хода (ревью #662).
	Titles map[string]string `json:"titles,omitempty"`
	// Deleted — удаленные чаты ящика: вопрос, отправленный до удаления, не
	// заводит новый чат окна и не тратит ход (ревью #665).
	// ponytail: по записи на удаленный чат (id ~16 байт); станет заметно —
	// забывать старше срока ящика.
	Deleted map[string]bool `json:"deleted,omitempty"`
	// Shared — чаты ящика, заведенные окном (#669), старые первыми.
	Shared []string `json:"shared,omitempty"`
}

// MaxSharedChats — сколько чатов окна держит ящик (решение владельца 17.11).
const MaxSharedChats = 50

// Exchange is one turn of a chat: вопрос и ответ.
type Exchange struct {
	Question string `json:"q"`
	Answer   string `json:"a"`
}

// WindowTurn is a turn the person made in the window (#669): Chat — чат окна,
// Last — только что сделанный обмен. New — чат заведен окном после выкатки
// #669: без связи с ящиком он заводится в ящике целиком (историю дает
// Relay.HistoryOf). Старые чаты окна (New = false) на телефон не уходят
// (решение владельца 17.11).
type WindowTurn struct {
	Chat  string
	Title string
	New   bool
	Last  Exchange
}

// maxTitles bounds State.Titles: названия чатов, которых окно еще не знает.
const maxTitles = 50

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
	// Desk — состояние окна для телефона (nil — отказ словами).
	Desk DeskInfo
	// Files и Hear — голос телефона (#648); nil — отказ DeskUnsupported.
	Files VoiceFiles
	Hear  Hearer
	// HearTimeout bounds one recognition; 0 — DefaultHearTimeout.
	HearTimeout time.Duration
	// Open — расшифровка кусков файлов (#650); nil — отказ DeskUnsupported.
	Open FileOpener
	// Chats — переименование и удаление чатов окна с телефона (#649); nil — отказ.
	Chats ChatEditor
	// Picker — выбор модели и источников с телефона (#657); nil — выбор мимо.
	Picker Picker
	// Turns — ходы человека в окне для телефонов (#669).
	Turns []WindowTurn
	// HistoryOf reads a window chat's exchanges when the mailbox opens it
	// (ревью #675: в очереди — только последний обмен); nil или сбой — Last.
	HistoryOf func(chat string) ([]Exchange, error)
	// Shared — ходы Turns легли в сохраненные исходящие: до этой отметки круг
	// их не взял, и работник отдаст их следующему кругу (ревью #675).
	Shared func()
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
	if err := r.share(&st); err != nil {
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
	if st.Deleted[m.Chat] {
		return Payload{}, false // чат удален: поздний вопрос не заводит его заново
	}
	var p Payload
	if json.Unmarshal(o.Plain, &p) != nil || strings.TrimSpace(p.Text) == "" {
		return Payload{}, false
	}
	switch p.Kind {
	case KindQuestion, KindSearch, KindOpen, KindMemory, KindDesk, KindRename, KindDelete:
		return p, true
	case KindVoice: // режим — только известный, запись — названа
		return p, (p.Text == VoiceDictate || p.Text == VoiceAsk) && p.File != nil
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
		reply = r.question(ctx, st, m.Chat, req)
	case KindMemory:
		reply = r.decide(ctx, st, m.Chat, req)
	case KindDesk:
		reply = r.desk(ctx)
	case KindRename, KindDelete:
		reply = r.editChat(ctx, st, m.Chat, req)
	case KindVoice:
		reply = r.voice(ctx, st, m.Chat, req)
	default:
		reply = r.find(ctx, req)
	}
	reply.Text = trim(reply.Text, r.MaxText)
	st.DeskCounter++
	// Push — итогу хода модели, и отказу модели тоже; тишина и отказ голоса —
	// не ход (ревью #660): расслышанное есть только у хода.
	asked := req.Kind == KindQuestion || req.Kind == KindVoice && reply.Heard != ""
	out := Outgoing{Chat: m.Chat, Counter: st.DeskCounter, Payload: reply, Notify: asked}
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
// question runs a phone question, with its file when attached (#650).
func (r Relay) question(ctx context.Context, st *State, chat string, req Payload) Payload {
	// Выбор телефона (#657) — до хода, в чате, который окно уже знает: в новом
	// выбирать не из чего. Отказ окна — словами и без хода.
	var picked *Pick
	if w := st.Chats[chat]; req.Pick != nil && w != "" && r.Picker != nil {
		if err := r.Picker.SetPick(ctx, w, *req.Pick); err != nil {
			return Payload{Kind: KindError, Text: err.Error()}
		}
		picked = req.Pick
	}
	reply := r.questionTurn(ctx, st, chat, req)
	if reply.Kind == KindAnswer {
		reply.Pick = picked // подтверждение: телефон снимает «ушло» только по нему
	}
	return reply
}

// questionTurn runs the phone's question, with its file if any.
func (r Relay) questionTurn(ctx context.Context, st *State, chat string, req Payload) Payload {
	if req.Attach == nil {
		return r.ask(ctx, st, chat, req.Text, nil)
	}
	if _, ok := r.Asker.(AttachedAsker); !ok {
		return Payload{Kind: KindError, Text: DeskUnsupported}
	}
	files, code := r.attached(ctx, req.Attach)
	if code != "" {
		return Payload{Kind: KindError, Text: code}
	}
	return r.ask(ctx, st, chat, req.Text, files)
}

// maxAttachPieces bounds the pieces of one file: 10 МБ кусками по 3 МБ — 4.
const maxAttachPieces = 5

// attached fetches, checks and opens the pieces of the phone's file (#650):
// каждый — id, размер и хеш из аутентичного сообщения до расшифровки, по
// порядку сообщения; итог — длина из сообщения. Сошлось все — куски
// удаляются; нет — остаются (их удалит правило бакета) и отказ кодом.
func (r Relay) attached(ctx context.Context, att *Attachment) ([]File, string) {
	if r.Files == nil || r.Open == nil {
		return nil, DeskUnsupported
	}
	if att.Size > MaxAttachment || len(att.Pieces) > maxAttachPieces {
		return nil, AttachTooLarge
	}
	data := make([]byte, 0, max(att.Size, 0))
	ids := make([]string, 0, len(att.Pieces))
	for _, p := range att.Pieces {
		sealed, code := r.piece(ctx, p)
		if code != "" {
			return nil, code
		}
		plain, err := r.Open.OpenFile(sealed)
		if err != nil {
			r.logf("mailrelay: attach: open %s: %v", p.ID, err)
			return nil, VoiceMismatch
		}
		if data = append(data, plain...); len(data) > att.Size {
			return nil, VoiceMismatch
		}
		ids = append(ids, p.ID)
	}
	if len(data) != att.Size || len(ids) == 0 {
		return nil, VoiceMismatch
	}
	r.dropFiles(ctx, ids...)
	return []File{{Name: att.Name, Type: att.Type, Data: data}}, ""
}

// piece fetches one sealed piece and checks it against its reference.
func (r Relay) piece(ctx context.Context, p FileRef) ([]byte, string) {
	if domain.ValidateMailFileID(p.ID) != nil {
		return nil, VoiceGone
	}
	sealed, err := r.Files.Get(ctx, p.ID)
	if err != nil {
		r.logf("mailrelay: file: get %s: %v", p.ID, err)
		return nil, VoiceGone
	}
	sum := sha256.Sum256(sealed)
	if len(sealed) != p.Size || hex.EncodeToString(sum[:]) != p.SHA256 {
		return nil, VoiceMismatch
	}
	return sealed, ""
}

// dropFiles deletes pieces the window has taken, со своим сроком: закрытие
// окна посреди хода не оставит их в бакете.
func (r Relay) dropFiles(ctx context.Context, ids ...string) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, id := range ids {
		if err := r.Files.Delete(dctx, id); err != nil {
			r.logf("mailrelay: file: delete %s: %v", id, err)
		}
	}
}

func (r Relay) ask(ctx context.Context, st *State, chat, question string, files []File) Payload {
	var a Answer
	var err error
	// Одна ветка для хода с шагами и с файлами (ревью #661): окно умеет оба.
	if aa, ok := r.Asker.(AttachedAsker); ok && (len(files) > 0 || r.StepEvery > 0) {
		var step func(Step)
		stop := func() {}
		if r.StepEvery > 0 {
			step, stop = r.stepper(ctx, st, chat)
		}
		a, err = aa.AskAttached(ctx, st.Chats[chat], question, files, step)
		stop()
	} else if sa, ok := r.Asker.(SteppedAsker); ok && r.StepEvery > 0 {
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
	if slices.Contains(st.Shared, chat) { // работа с телефона освежает место чата окна (ревью #675)
		touchShared(st, chat)
	}
	r.pendingTitle(ctx, st, chat)
	return Payload{Kind: KindAnswer, Text: a.Text, Sources: a.Sources, Context: a.Context, Only: a.Only}
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
		if n.Payload.Kind == KindDelete { // окно удалило чат: связь и предложенное забыть (#649)
			forgetChat(st, chat)
		}
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

// share queues the window's turns for the phones (#669): связанный чат — только
// новый ход; новый чат окна без связи — заводится в ящике историей пачками
// (предел отправки сервера — раз в секунду, ревью #675). Чатов окна в ящике —
// MaxSharedChats последних: самый старый уходит с телефонов delete «evicted»,
// в окне остается. Удаление и переименование того же круга (Notes) — в силе.
func (r Relay) share(st *State) error {
	if len(r.Turns) == 0 {
		return nil
	}
	gone, titles := sameRoundEdits(r.Notes)
	for _, t := range r.Turns {
		if gone[t.Chat] {
			continue
		}
		if nt, ok := titles[t.Chat]; ok {
			t.Title = nt
		}
		r.shareTurn(st, t)
		r.evict(st)
	}
	if err := r.Store.Save(*st); err != nil {
		return fmt.Errorf("mailrelay: state: %w", err)
	}
	if r.Shared != nil {
		r.Shared()
	}
	return nil
}

// history is window chat t's exchanges for a new mailbox chat: HistoryOf или,
// если ее нет или чат не прочитан, — хотя бы последний обмен.
func (r Relay) history(t WindowTurn) []Exchange {
	if r.HistoryOf != nil {
		h, err := r.HistoryOf(t.Chat)
		if err == nil && len(h) > 0 {
			return h
		}
		if err != nil {
			r.logf("mailrelay: history %s: %v", t.Chat, err)
		}
	}
	return []Exchange{t.Last}
}

// sameRoundEdits are the window's deletes and renames of this round by chat.
func sameRoundEdits(notes []Note) (gone map[string]bool, titles map[string]string) {
	gone, titles = map[string]bool{}, map[string]string{}
	for _, n := range notes {
		switch n.Payload.Kind {
		case KindDelete:
			gone[n.Chat] = true
		case KindRename:
			titles[n.Chat] = n.Payload.Text
		}
	}
	return gone, titles
}

// shareTurn queues one window turn: связанный — новый ход, новый — история.
func (r Relay) shareTurn(st *State, t WindowTurn) {
	chat := mailChat(st, t.Chat)
	if chat == "" {
		if !t.New {
			return
		}
		chat = newShareChat()
		if st.Chats == nil {
			st.Chats = map[string]string{}
		}
		st.Chats[chat] = t.Chat
		for _, pack := range r.historyPacks(r.history(t)) {
			r.queueDesk(st, chat, Payload{Kind: KindHistory, Title: t.Title, History: pack})
		}
	} else {
		x := t.Last
		r.queueDesk(st, chat, Payload{Kind: KindAsked, Text: x.Question, Title: t.Title})
		r.queueDesk(st, chat, Payload{Kind: KindAnswer, Text: trim(x.Answer, r.MaxText)})
	}
	if t.New || slices.Contains(st.Shared, chat) {
		touchShared(st, chat)
	}
}

// touchShared moves a window chat to the fresh end of State.Shared.
func touchShared(st *State, chat string) {
	st.Shared = append(slices.DeleteFunc(st.Shared, func(c string) bool { return c == chat }), chat)
}

// evict keeps MaxSharedChats window chats: самый старый — delete «evicted» на
// телефоны и без связи, но не «удален»: вопрос телефона, ушедший до
// вытеснения, получит ответ новым чатом окна, а не пропадет (ревью #675).
func (r Relay) evict(st *State) {
	for len(st.Shared) > MaxSharedChats {
		old := st.Shared[0]
		st.Shared = st.Shared[1:]
		r.queueDesk(st, old, Payload{Kind: KindDelete, Text: DeleteEvicted})
		delete(st.Chats, old)
		delete(st.Offered, old)
		delete(st.Titles, old)
	}
}

// historyPacks splits the history into packs whose JSON fits MaxText; ответ,
// не влезающий и в пустую пачку, укорочен (MaxText 0 — одна пачка).
func (r Relay) historyPacks(h []Exchange) [][]Exchange {
	if r.MaxText <= 0 {
		return [][]Exchange{h}
	}
	size := func(xs []Exchange) int { b, _ := json.Marshal(xs); return len(b) }
	var packs [][]Exchange
	var cur []Exchange
	for _, x := range h {
		for size([]Exchange{x}) > r.MaxText && len(x.Answer) > len(trimNote) {
			x.Answer = trim(x.Answer, max(len(x.Answer)/2, len(trimNote)))
		}
		if len(cur) > 0 && size(append(slices.Clone(cur), x)) > r.MaxText {
			packs, cur = append(packs, cur), nil
		}
		cur = append(cur, x)
	}
	return append(packs, cur)
}

// newShareChat is a mailbox chat for a window chat: 16 hex, как у телефона.
func newShareChat() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read не возвращает ошибку с Go 1.24
	return hex.EncodeToString(b)
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

// DefaultHearTimeout bounds one recognition: повисший сервер распознавания не
// держит ящик телефона — работник один на все серверы (ревью #660).
const DefaultHearTimeout = 2 * time.Minute

// voice hears the phone's recording (#648): кусок — из файлов ящика, сверка
// id, размера и хеша из аутентичного сообщения до расшифровки; совпал —
// удаляется сразу, до хода модели (не совпал — не удаляется: это могла быть
// ждущая запись другого телефона). Отказы — кодами, причины — в журнал окна:
// в них пути компьютера (ревью #660). Диктовка — текст телефону; вопрос
// голосом — ход модели с расслышанным; тишина — «не расслышал».
func (r Relay) voice(ctx context.Context, st *State, chat string, req Payload) Payload {
	if r.Files == nil || r.Hear == nil {
		return Payload{Kind: KindError, Text: DeskUnsupported}
	}
	sealed, code := r.piece(ctx, *req.File)
	if code != "" {
		return Payload{Kind: KindError, Text: code}
	}
	r.dropFiles(ctx, req.File.ID)
	hctx, cancel := context.WithTimeout(ctx, cmp.Or(r.HearTimeout, DefaultHearTimeout))
	text, err := r.Hear.Hear(hctx, sealed)
	cancel()
	if err != nil {
		r.logf("mailrelay: voice: hear: %v", err)
		return Payload{Kind: KindError, Text: VoiceUnheard}
	}
	text = strings.TrimSpace(text)
	switch {
	case req.Text != VoiceAsk:
		return Payload{Kind: KindHeard, Text: text}
	case text == "": // тишина вопроса — свой код: «heard» — ответ диктовке (повторное ревью #660)
		return Payload{Kind: KindError, Text: VoiceSilent}
	}
	reply := r.ask(ctx, st, chat, text, nil)
	reply.Heard = text
	return reply
}

// maxChatTitle bounds a title from the phone, в знаках: столько же держит
// название окна (title в desktop), длиннее телефон не обещает (ревью #662).
const maxChatTitle = 60

// editChat renames or deletes the window's chat of a phone chat (#649): чат
// один в двух местах. Окно чата еще не знает — подтверждение без правки;
// удаленный — забывается, ответы туда не пойдут.
func (r Relay) editChat(ctx context.Context, st *State, chat string, req Payload) Payload {
	done := Payload{Kind: KindEdited, Text: req.Kind}
	title := []rune(strings.TrimSpace(req.Text))
	title = title[:min(len(title), maxChatTitle)]
	id := st.Chats[chat]
	if id == "" { // окно чата еще не знает: название ждет первого хода
		if req.Kind == KindRename {
			if st.Titles == nil {
				st.Titles = map[string]string{}
			}
			if _, ok := st.Titles[chat]; ok || len(st.Titles) < maxTitles {
				st.Titles[chat] = string(title)
			}
		} else {
			delete(st.Titles, chat)
		}
		return done
	}
	if r.Chats == nil {
		return Payload{Kind: KindError, Text: DeskUnsupported}
	}
	var err error
	if req.Kind == KindRename {
		err = r.Chats.RenameChat(ctx, id, string(title))
	} else {
		err = r.Chats.DeleteChat(ctx, id)
	}
	if err != nil {
		r.logf("mailrelay: %s %s: %v", req.Kind, id, err)
		return Payload{Kind: KindError, Text: ChatEditFailed}
	}
	if req.Kind == KindDelete {
		// Удаление — всем телефонам ключа тем же кругом, а не заметкой окна:
		// заметки теряются при сбое круга (ревью #665). Эхо окна пройдет
		// мимо — связи уже нет.
		r.queueDesk(st, chat, Payload{Kind: KindDelete, Text: "chat"})
		forgetChat(st, chat)
	}
	return done
}

// forgetChat drops everything the relay keeps for a deleted mailbox chat,
// кроме Seen: он защищает от повтора старых сообщений.
func forgetChat(st *State, chat string) {
	delete(st.Chats, chat)
	delete(st.Offered, chat)
	delete(st.Titles, chat)
	st.Shared = slices.DeleteFunc(st.Shared, func(c string) bool { return c == chat })
	if st.Deleted == nil {
		st.Deleted = map[string]bool{}
	}
	st.Deleted[chat] = true
}

// queueDesk seals p from the window into chat's outbox.
func (r Relay) queueDesk(st *State, chat string, p Payload) {
	st.DeskCounter++
	out := Outgoing{Chat: chat, Counter: st.DeskCounter, Payload: p}
	var err error
	if out.Sealed, err = r.sealFitting(&out); err != nil {
		r.logf("mailrelay: %s: %v", p.Kind, err)
		return
	}
	st.Outbox = append(st.Outbox, out)
}

// pendingTitle puts the phone's early title on the window's new chat.
func (r Relay) pendingTitle(ctx context.Context, st *State, chat string) {
	t, ok := st.Titles[chat]
	if !ok {
		return
	}
	delete(st.Titles, chat)
	if r.Chats == nil {
		return
	}
	if err := r.Chats.RenameChat(ctx, st.Chats[chat], t); err != nil {
		r.logf("mailrelay: rename %s: %v", st.Chats[chat], err)
	}
}

// desk tells the phone the window's state (#647): модель и названия источников.
func (r Relay) desk(ctx context.Context) Payload {
	if r.Desk == nil {
		return Payload{Kind: KindError, Text: DeskUnsupported}
	}
	st, err := r.Desk.State(ctx)
	if err != nil {
		return Payload{Kind: KindError, Text: err.Error()}
	}
	return Payload{Kind: KindDesk, Desk: &st}
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
