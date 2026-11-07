// Package chat answers a question over the person's documents (#436): поиск
// по источникам дает документы, модель отвечает по ним со ссылками. Модель и
// поиск — порты: окно подставляет общую выдачу источников и клиента
// OpenAI-совместимого API (Vercel AI Gateway, Ollama).
package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Ошибки хода: окно называет причину своими словами.
var (
	ErrEmptyQuestion = errors.New("empty question")
	ErrSearch        = errors.New("search failed")
	ErrModel         = errors.New("model failed")
	ErrNoReader      = errors.New("tools mode needs a document reader")
)

// Role is who said a message.
type Role string

// Роли сообщений, как их понимают OpenAI-совместимые API.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Mode is who finds the documents for the answer (решение владельца 06.11).
type Mode string

const (
	// ModeContext — окно ищет само и кладет найденное в запрос: для
	// маленьких локальных моделей, которые плохо зовут инструменты.
	ModeContext Mode = "context"
	// ModeTools — модель сама зовет поиск и чтение, сколько нужно: для
	// сильных моделей.
	ModeTools Mode = "tools"
)

// Message is one line of the conversation as the person sees it.
type Message struct {
	Role Role
	Text string
}

// Doc is a found document: запись базы или файл. Ref — по нему окно открывает
// документ («kb:12», «file:/путь»).
type Doc struct {
	Ref, Title, Source, Text string
	// SourceID — id источника окна (папка, каталог, kb-сервер): по нему чат
	// сужается до отмеченных источников и красит значок документа.
	SourceID string
}

// Tool is a function the model may call.
type Tool struct {
	Name, Description string
	Parameters        []byte // JSON Schema аргументов
}

// ToolCall is the model asking to run a tool.
type ToolCall struct {
	ID, Name, Arguments string
}

// ModelMessage is one message of a model request.
type ModelMessage struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // у ответа модели, что зовет инструменты
	ToolCallID string     // у результата инструмента
}

// ModelReply is what the model answered: текст или вызовы инструментов.
type ModelReply struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage // счет провайдера; нули — сервер его не прислал
}

// Usage is the tokens the provider counted for one reply.
type Usage struct {
	In, Out int
}

// Searcher finds documents for a query across the person's sources.
type Searcher interface {
	Search(ctx context.Context, q string, limit int) ([]Doc, error)
}

// Reader opens one document by its Ref for the read tool.
type Reader interface {
	Read(ctx context.Context, ref string) (Doc, error)
}

// Model completes a conversation.
type Model interface {
	Complete(ctx context.Context, msgs []ModelMessage, tools []Tool) (ModelReply, error)
}

// Streamer is a Model that shows its answer while writing it: show получает
// текст текущего ответа, накопленный к этому куску.
type Streamer interface {
	Stream(ctx context.Context, msgs []ModelMessage, tools []Tool, show func(text string)) (ModelReply, error)
}

// Turn is one answered question: ответ и документы, с которыми работали.
type Turn struct {
	Answer string
	Docs   []Doc
}

// Сколько документов и текста уходит модели в режиме «окно ищет»: маленькие
// локальные модели держат 4–8 тыс. токенов контекста.
const (
	MaxDocs     = 8
	MaxDocRunes = 1500
	// MaxAttachedRunes — потолок приложенного документа: его человек приложил,
	// чтобы модель прочла, поэтому больше, чем у найденного.
	//
	// ponytail: один потолок на любую модель. Потолок: маленькая локальная
	// модель с контекстом 4 тыс. токенов и два приложенных документа. Путь:
	// потолок от размера контекста модели в настройках чата.
	MaxAttachedRunes = 12000
)

// Request is one question: разговор до него, режим, приложенные человеком
// документы (файл, найденное, ссылка) и показ ответа по мере генерации
// (Show; nil — без показа).
type Request struct {
	History  []Message
	Question string
	Mode     Mode
	Attached []Doc
	Show     func(text string)
	// About — портрет человека, одобренный им (#479); пусто — без портрета.
	About string
	// Remember offers a fact about the person for approval (инструмент
	// «запомнить» режима «модель ищет»); nil — портрет не ведется, инструмента нет.
	Remember func(ctx context.Context, section, text string) error
}

// Service answers questions.
type Service struct {
	search Searcher
	model  Model
	reader Reader // nil — режим «модель ищет» недоступен
}

// Option configures a Service.
type Option func(*Service)

// WithReader lets the model read documents by Ref (режим «модель ищет»).
func WithReader(r Reader) Option { return func(s *Service) { s.reader = r } }

// NewService wires the search and the model.
func NewService(s Searcher, m Model, opts ...Option) *Service {
	svc := &Service{search: s, model: m}
	for _, o := range opts {
		o(svc)
	}
	return svc
}

const systemPrompt = `Ты отвечаешь на вопросы человека по его документам: записям базы знаний и файлам.
Отвечай только по документам ниже. Ссылайся на них номерами в квадратных скобках, например [1].
Если в документах ответа нет — так и скажи и не выдумывай. Отвечай на языке вопроса.`

// Answer answers question after history.
func (s *Service) Answer(ctx context.Context, history []Message, question string, mode Mode) (Turn, error) {
	return s.Respond(ctx, Request{History: history, Question: question, Mode: mode})
}

// AnswerStream is Answer that shows the answer while the model writes it.
func (s *Service) AnswerStream(ctx context.Context, history []Message, question string, mode Mode, show func(text string)) (Turn, error) {
	return s.Respond(ctx, Request{History: history, Question: question, Mode: mode, Show: show})
}

// Respond answers r. Show получает растущий текст, а пустую строку — когда
// показанное оказалось не ответом (шаг кончился вызовом инструмента). Модель
// без потока отвечает целиком, и Show не зовется. Приложенное идет модели
// первым и остается в документах хода.
//
// ponytail: поиск идет по одному последнему вопросу. Потолок: уточнение
// «а сколько он стоит?» без существительного находит не то. Путь: режим
// «модель ищет» (сама формулирует запрос) или переписывание запроса моделью.
func (s *Service) Respond(ctx context.Context, r Request) (Turn, error) {
	if _, ok := s.model.(Streamer); !ok {
		r.Show = nil
	}
	r.Question = strings.TrimSpace(r.Question)
	if r.Question == "" {
		return Turn{}, ErrEmptyQuestion
	}
	switch r.Mode {
	case ModeContext:
	case ModeTools:
		return s.answerWithTools(ctx, r)
	default:
		return Turn{}, fmt.Errorf("%w: unknown mode %q", ErrModel, r.Mode)
	}
	docs := r.Attached
	if limit := MaxDocs - len(r.Attached); limit > 0 {
		found, err := s.search.Search(ctx, r.Question, limit)
		if err != nil {
			return Turn{}, fmt.Errorf("%w: %w", ErrSearch, err)
		}
		docs = appendNew(slices.Clone(r.Attached), found...)
	}
	msgs := []ModelMessage{{Role: RoleSystem, Content: withAbout(systemPrompt, r.About)}}
	for _, m := range r.History {
		msgs = append(msgs, ModelMessage{Role: m.Role, Content: m.Text})
	}
	msgs = append(msgs, ModelMessage{Role: RoleUser, Content: documentsBlock(docs, len(r.Attached)) + "\n\nВопрос: " + r.Question})
	reply, err := s.complete(ctx, msgs, nil, r.Show)
	if err != nil {
		return Turn{}, fmt.Errorf("%w: %w", ErrModel, err)
	}
	if strings.TrimSpace(reply.Content) == "" {
		return Turn{}, fmt.Errorf("%w: empty answer", ErrModel)
	}
	used := cited(reply.Content, docs)
	if len(r.Attached) > 0 && len(used) < len(docs) {
		used = appendNew(slices.Clone(r.Attached), used...)
	}
	return Turn{Answer: reply.Content, Docs: used}, nil
}

// withAbout adds the person's portrait to the system prompt.
func withAbout(prompt, about string) string {
	if strings.TrimSpace(about) == "" {
		return prompt
	}
	return prompt + "\n\nО человеке (портрет, подтвержденный им; учитывай в ответе, но не пересказывай):\n" + about
}

// complete asks the model, by stream when show is set (тогда модель — Streamer).
func (s *Service) complete(ctx context.Context, msgs []ModelMessage, tools []Tool, show func(string)) (ModelReply, error) {
	if show != nil {
		return s.model.(Streamer).Stream(ctx, msgs, tools, show)
	}
	return s.model.Complete(ctx, msgs, tools)
}

// documentsBlock numbers the documents for the model; первые attached —
// приложенные, у них свой потолок текста.
func documentsBlock(docs []Doc, attached int) string {
	if len(docs) == 0 {
		return "Документов по вопросу не нашлось."
	}
	var b strings.Builder
	b.WriteString("Документы:")
	for i, d := range docs {
		limit := MaxDocRunes
		if i < attached {
			limit = MaxAttachedRunes
		}
		fmt.Fprintf(&b, "\n\n[%d] %s", i+1, d.Title)
		if d.Source != "" {
			fmt.Fprintf(&b, " (%s)", d.Source)
		}
		b.WriteString("\n" + trimRunes(d.Text, limit))
	}
	return b.String()
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var citation = regexp.MustCompile(`\[(\d+)\]`)

// cited returns the documents the answer refers to, in order of first
// mention; без ссылок — все найденные: человек видит, на чем отвечали.
func cited(answer string, docs []Doc) []Doc {
	var out []Doc
	seen := map[int]bool{}
	for _, m := range citation.FindAllStringSubmatch(answer, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > len(docs) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, docs[n-1])
	}
	if len(out) == 0 {
		return docs
	}
	return out
}
