package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Пределы режима «модель ищет»: сколько раз модель может позвать инструменты
// за один вопрос и сколько текста документа отдает чтение.
const (
	MaxSteps     = 6
	MaxReadRunes = 8000
	searchSnip   = 300
)

const toolsPrompt = `Ты отвечаешь на вопросы человека по его документам: записям базы знаний и файлам.
Ищи инструментом search и читай нужное инструментом read, сколько понадобится.
Отвечай только по найденному и называй документы, на которые опираешься. Если ответа нет — так и скажи.
Отвечай на языке вопроса.`

// rememberPrompt is added when the person keeps a portrait.
const rememberPrompt = `
Если в разговоре видно устойчивое о самом человеке (кто он, работа, планы, как с ним говорить) и этого нет в портрете — предложи запомнить инструментом remember. Содержание документов и разовые просьбы не запоминай.`

// rememberTool offers a fact for the portrait (#479): человек одобрит или нет.
// Разделы — из домена: новый раздел модель видит без правки здесь.
var rememberTool = func() Tool {
	sections := []string{}
	for _, s := range domain.FactSections() {
		sections = append(sections, string(s))
	}
	enum, _ := json.Marshal(sections) // срез строк Marshal не отвергает
	return Tool{Name: "remember", Description: "Предложить запомнить устойчивый факт о человеке. Факт ждет одобрения человека и в портрет без него не попадет.",
		Parameters: []byte(`{"type":"object","properties":{"section":{"type":"string","enum":` + string(enum) + `},"text":{"type":"string","description":"факт одной фразой от третьего лица"}},"required":["section","text"]}`)}
}()

var tools = []Tool{
	{Name: "search", Description: "Поиск по базе знаний, kb-серверам и файлам человека. Возвращает документы с ref, заголовком и началом текста.",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"запрос"}},"required":["q"]}`)},
	{Name: "read", Description: "Прочитать документ целиком по ref из результата search.",
		Parameters: []byte(`{"type":"object","properties":{"ref":{"type":"string"}},"required":["ref"]}`)},
}

// toolArgs — аргументы вызова инструмента, как их присылает модель.
type toolArgs struct{ Q, Ref, Section, Text, Which, Description, Note string }

// toolRun is one question in tools mode: что найдено и что прочитано.
type toolRun struct {
	s        *Service
	attached []Doc // приложенное человеком: в документах хода всегда, первым
	found    []Doc
	read     []Doc
	remember func(ctx context.Context, section, text string) error
	versions Versions
	propose  func(ctx context.Context, p VersionProposal) error
}

// answerWithTools lets the model search and read until it answers. После
// MaxSteps шагов последний запрос идет без инструментов — модель обязана
// ответить по тому, что уже видела.
func (s *Service) answerWithTools(ctx context.Context, r Request) (Turn, error) {
	if s.reader == nil {
		return Turn{}, ErrNoReader
	}
	msgs := toolsMessages(r)
	run := &toolRun{s: s, attached: r.Attached, remember: r.Remember, versions: r.Versions, propose: r.ProposeVersion}
	offered := offeredTools(r)
	for step := 0; ; step++ {
		offer := offered
		if step == MaxSteps {
			// Без слов модель на последнем шаге снова зовет инструмент и молчит (живая проверка 12.11).
			offer = nil
			msgs = append(msgs, ModelMessage{Role: RoleUser, Content: lastStepPrompt})
		}
		reply, err := s.complete(ctx, msgs, offer, r.Show)
		if err != nil {
			return Turn{}, fmt.Errorf("%w: %w", ErrModel, err)
		}
		if len(reply.ToolCalls) == 0 || offer == nil {
			if strings.TrimSpace(reply.Content) == "" && offer == nil {
				return Turn{}, fmt.Errorf("%w: %w", ErrModel, ErrOutOfSteps)
			}
			if strings.TrimSpace(reply.Content) == "" {
				return Turn{}, fmt.Errorf("%w: empty answer", ErrModel)
			}
			return Turn{Answer: reply.Content, Docs: run.docs(), PromptTokens: promptTokens(reply.Usage)}, nil
		}
		if r.Show != nil {
			r.Show("") // «сейчас поищу» — не ответ
		}
		msgs = append(msgs, ModelMessage{Role: RoleAssistant, Content: reply.Content, ToolCalls: reply.ToolCalls})
		for _, c := range reply.ToolCalls {
			msgs = append(msgs, ModelMessage{Role: RoleTool, ToolCallID: c.ID, Content: run.exec(ctx, c)})
		}
	}
}

// lastStepPrompt asks for the answer when the steps are over.
const lastStepPrompt = "Шаги поиска кончились, инструментов больше нет: ответь сейчас по тому, что уже видел. Если ответа нет, так и скажи и предложи, что уточнить."

// toolsMessages is the start of a tools-mode turn: правила (с «запомнить»,
// если портрет ведется), прежний разговор и вопрос с приложенным.
func toolsMessages(r Request) []ModelMessage {
	prompt := toolsPrompt
	if r.Remember != nil {
		prompt += rememberPrompt
	}
	if r.Versions != nil {
		prompt += versionsPrompt
	}
	if r.ProposeVersion != nil {
		prompt += proposeVersionPrompt
	}
	msgs := []ModelMessage{{Role: RoleSystem, Content: withAbout(prompt, r.About)}}
	for _, m := range r.History {
		msgs = append(msgs, ModelMessage{Role: m.Role, Content: m.Text})
	}
	question := r.Question
	if len(r.Attached) > 0 {
		question = "Приложенные человеком документы:" + strings.TrimPrefix(documentsBlock(r.Attached, len(r.Attached)), "Документы:") + "\n\nВопрос: " + question
	}
	return append(msgs, ModelMessage{Role: RoleUser, Content: question})
}

// offeredTools: поиск и чтение всегда, версии — когда у окна они есть,
// «запомнить» — когда портрет ведется.
func offeredTools(r Request) []Tool {
	offered := slices.Clone(tools)
	if r.Versions != nil {
		offered = append(offered, versionTools...)
	}
	if r.ProposeVersion != nil {
		offered = append(offered, proposeVersionTool)
	}
	if r.Remember != nil {
		offered = append(offered, rememberTool)
	}
	return offered
}

// exec runs one tool call; ошибка уходит модели текстом — ход продолжается.
func (r *toolRun) exec(ctx context.Context, c ToolCall) string {
	var args toolArgs
	if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
		return "ошибка: аргументы не разобраны: " + err.Error()
	}
	switch c.Name {
	case "search":
		docs, err := r.s.search.Search(ctx, args.Q, MaxDocs)
		if err != nil {
			return "ошибка поиска: " + err.Error()
		}
		r.found = appendNew(r.found, docs...)
		return searchResult(docs)
	case "read":
		d, err := r.s.reader.Read(ctx, args.Ref)
		if err != nil {
			return "ошибка чтения " + args.Ref + ": " + err.Error()
		}
		r.read = appendNew(r.read, d)
		if n := len([]rune(d.Text)); n > MaxReadRunes {
			return d.Title + "\n" + trimRunes(d.Text, MaxReadRunes) + "\n" + partialNote("документ", n)
		}
		return d.Title + "\n" + d.Text
	case "versions", "open_version", "propose_version":
		return r.execVersionTool(ctx, c.Name, args)
	case "remember":
		if r.remember == nil {
			return "ошибка: портрет не ведется"
		}
		if err := r.remember(ctx, args.Section, args.Text); err != nil {
			return "не предложено: " + err.Error()
		}
		return "предложено: ждет одобрения человека, в портрет без него не попадет"
	default:
		return "ошибка: неизвестный инструмент " + c.Name
	}
}

// execVersionTool runs the tools about the versions of documents.
func (r *toolRun) execVersionTool(ctx context.Context, name string, a toolArgs) string {
	if name == "propose_version" {
		return r.proposeVersion(ctx, VersionProposal{Family: strings.TrimSpace(a.Q), Text: a.Text, Description: strings.TrimSpace(a.Description), Note: strings.TrimSpace(a.Note)})
	}
	if r.versions == nil {
		return "ошибка: версии документов недоступны"
	}
	if name == "versions" {
		return r.listVersions(ctx, a.Q)
	}
	return r.openVersion(ctx, a.Q, a.Which)
}

// docs are the documents of the turn: прочитанные, а без чтения — найденные.
func (r *toolRun) docs() []Doc {
	used := r.found
	if len(r.read) > 0 {
		used = r.read
	}
	return appendNew(slices.Clone(r.attached), used...)
}

func searchResult(docs []Doc) string {
	if len(docs) == 0 {
		return "ничего не найдено"
	}
	var b strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&b, "ref: %s\nзаголовок: %s\n", d.Ref, d.Title)
		if d.Source != "" {
			fmt.Fprintf(&b, "источник: %s\n", d.Source)
		}
		fmt.Fprintf(&b, "начало: %s\n\n", trimRunes(d.Text, searchSnip))
	}
	return b.String()
}

// appendNew appends the docs not yet in list (по Ref).
func appendNew(list []Doc, docs ...Doc) []Doc {
	for _, d := range docs {
		if !slices.ContainsFunc(list, func(x Doc) bool { return x.Ref == d.Ref }) {
			list = append(list, d)
		}
	}
	return list
}
