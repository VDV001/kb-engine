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
)

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
//
// ponytail: поиск идет по одному последнему вопросу. Потолок: уточнение
// «а сколько он стоит?» без существительного находит не то. Путь: режим
// «модель ищет» (сама формулирует запрос) или переписывание запроса моделью.
func (s *Service) Answer(ctx context.Context, history []Message, question string, mode Mode) (Turn, error) {
	return s.AnswerStream(ctx, history, question, mode, nil)
}

// AnswerStream is Answer that shows the answer while the model writes it:
// show получает растущий текст, а пустую строку — когда показанное оказалось
// не ответом (шаг кончился вызовом инструмента). Модель без потока отвечает
// целиком, и show не зовется.
func (s *Service) AnswerStream(ctx context.Context, history []Message, question string, mode Mode, show func(text string)) (Turn, error) {
	if _, ok := s.model.(Streamer); !ok {
		show = nil
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return Turn{}, ErrEmptyQuestion
	}
	switch mode {
	case ModeContext:
	case ModeTools:
		return s.answerWithTools(ctx, history, question, show)
	default:
		return Turn{}, fmt.Errorf("%w: unknown mode %q", ErrModel, mode)
	}
	docs, err := s.search.Search(ctx, question, MaxDocs)
	if err != nil {
		return Turn{}, fmt.Errorf("%w: %w", ErrSearch, err)
	}
	msgs := []ModelMessage{{Role: RoleSystem, Content: systemPrompt}}
	for _, m := range history {
		msgs = append(msgs, ModelMessage{Role: m.Role, Content: m.Text})
	}
	msgs = append(msgs, ModelMessage{Role: RoleUser, Content: documentsBlock(docs) + "\n\nВопрос: " + question})
	reply, err := s.complete(ctx, msgs, nil, show)
	if err != nil {
		return Turn{}, fmt.Errorf("%w: %w", ErrModel, err)
	}
	if strings.TrimSpace(reply.Content) == "" {
		return Turn{}, fmt.Errorf("%w: empty answer", ErrModel)
	}
	return Turn{Answer: reply.Content, Docs: cited(reply.Content, docs)}, nil
}

// complete asks the model, by stream when show is set (тогда модель — Streamer).
func (s *Service) complete(ctx context.Context, msgs []ModelMessage, tools []Tool, show func(string)) (ModelReply, error) {
	if show != nil {
		return s.model.(Streamer).Stream(ctx, msgs, tools, show)
	}
	return s.model.Complete(ctx, msgs, tools)
}

// documentsBlock numbers the documents for the model.
func documentsBlock(docs []Doc) string {
	if len(docs) == 0 {
		return "Документов по вопросу не нашлось."
	}
	var b strings.Builder
	b.WriteString("Документы:")
	for i, d := range docs {
		fmt.Fprintf(&b, "\n\n[%d] %s", i+1, d.Title)
		if d.Source != "" {
			fmt.Fprintf(&b, " (%s)", d.Source)
		}
		b.WriteString("\n" + trimRunes(d.Text, MaxDocRunes))
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
