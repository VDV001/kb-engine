package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

var tools = []Tool{
	{Name: "search", Description: "Поиск по базе знаний, kb-серверам и файлам человека. Возвращает документы с ref, заголовком и началом текста.",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"запрос"}},"required":["q"]}`)},
	{Name: "read", Description: "Прочитать документ целиком по ref из результата search.",
		Parameters: []byte(`{"type":"object","properties":{"ref":{"type":"string"}},"required":["ref"]}`)},
}

// toolRun is one question in tools mode: что найдено и что прочитано.
type toolRun struct {
	s     *Service
	found []Doc
	read  []Doc
}

// answerWithTools lets the model search and read until it answers. После
// MaxSteps шагов последний запрос идет без инструментов — модель обязана
// ответить по тому, что уже видела.
func (s *Service) answerWithTools(ctx context.Context, r Request) (Turn, error) {
	if s.reader == nil {
		return Turn{}, ErrNoReader
	}
	msgs := []ModelMessage{{Role: RoleSystem, Content: toolsPrompt}}
	for _, m := range r.History {
		msgs = append(msgs, ModelMessage{Role: m.Role, Content: m.Text})
	}
	question := r.Question
	if len(r.Attached) > 0 {
		question = "Приложенные человеком документы:" + strings.TrimPrefix(documentsBlock(r.Attached, len(r.Attached)), "Документы:") + "\n\nВопрос: " + question
	}
	msgs = append(msgs, ModelMessage{Role: RoleUser, Content: question})
	run := &toolRun{s: s, read: slices.Clone(r.Attached)}
	for step := 0; ; step++ {
		offer := tools
		if step == MaxSteps {
			offer = nil
		}
		reply, err := s.complete(ctx, msgs, offer, r.Show)
		if err != nil {
			return Turn{}, fmt.Errorf("%w: %w", ErrModel, err)
		}
		if len(reply.ToolCalls) == 0 || offer == nil {
			if strings.TrimSpace(reply.Content) == "" {
				return Turn{}, fmt.Errorf("%w: empty answer", ErrModel)
			}
			return Turn{Answer: reply.Content, Docs: run.docs()}, nil
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

// exec runs one tool call; ошибка уходит модели текстом — ход продолжается.
func (r *toolRun) exec(ctx context.Context, c ToolCall) string {
	var args struct{ Q, Ref string }
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
		return d.Title + "\n" + trimRunes(d.Text, MaxReadRunes)
	default:
		return "ошибка: неизвестный инструмент " + c.Name
	}
}

// docs are the documents of the turn: прочитанные, а без чтения — найденные.
func (r *toolRun) docs() []Doc {
	if len(r.read) > 0 {
		return r.read
	}
	return r.found
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
