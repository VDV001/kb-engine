package chat_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// scriptModel answers with the replies in order and remembers each request.
type scriptModel struct {
	replies []chat.ModelReply
	asked   [][]chat.ModelMessage
	tools   [][]chat.Tool
}

func (m *scriptModel) Complete(_ context.Context, msgs []chat.ModelMessage, tools []chat.Tool) (chat.ModelReply, error) {
	m.asked = append(m.asked, append([]chat.ModelMessage(nil), msgs...))
	m.tools = append(m.tools, tools)
	if len(m.replies) == 0 {
		return chat.ModelReply{Content: "конец"}, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}

type fakeReader struct {
	docs map[string]chat.Doc
	read []string
}

func (f *fakeReader) Read(_ context.Context, ref string) (chat.Doc, error) {
	f.read = append(f.read, ref)
	d, ok := f.docs[ref]
	if !ok {
		return chat.Doc{}, errors.New("нет такого документа")
	}
	return d, nil
}

func call(id, name, args string) chat.ModelReply {
	return chat.ModelReply{ToolCalls: []chat.ToolCall{{ID: id, Name: name, Arguments: args}}}
}

// Режим «модель ищет» (сильные модели): модель сама зовет поиск и чтение,
// окно исполняет и возвращает результат; документы чата — прочитанные.
func TestToolsMode(t *testing.T) {
	s := &fakeSearch{docs: docs}
	r := &fakeReader{docs: map[string]chat.Doc{"kb:12": docs[0]}}
	m := &scriptModel{replies: []chat.ModelReply{
		call("c1", "search", `{"q":"срок поставки"}`),
		call("c2", "read", `{"ref":"kb:12"}`),
		{Content: "Срок — 30 дней (Договор поставки)."},
	}}
	turn, err := chat.NewService(s, m, chat.WithReader(r)).Answer(context.Background(), nil, "какой срок?", chat.ModeTools)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Answer != "Срок — 30 дней (Договор поставки)." || len(turn.Docs) != 1 || turn.Docs[0].Ref != "kb:12" {
		t.Fatalf("turn = %+v", turn)
	}
	if len(s.queries) != 1 || s.queries[0] != "срок поставки" || len(r.read) != 1 {
		t.Fatalf("search %v, read %v", s.queries, r.read)
	}
	// Результат поиска вернулся модели сообщением инструмента с тем же id и
	// называет ref, по которому читать.
	last := m.asked[1][len(m.asked[1])-1]
	if last.Role != chat.RoleTool || last.ToolCallID != "c1" || !strings.Contains(last.Content, "kb:12") {
		t.Fatalf("результат поиска = %+v", last)
	}
	if len(m.tools[0]) != 2 {
		t.Fatalf("модели предложено %d инструментов, ждали search и read", len(m.tools[0]))
	}
}

// Ошибка инструмента — не конец хода: модель узнает о ней и отвечает дальше.
// Неизвестный инструмент и битые аргументы — тоже ответ модели, а не паника.
func TestToolsMode_ToolErrorsGoBackToModel(t *testing.T) {
	m := &scriptModel{replies: []chat.ModelReply{
		call("c1", "read", `{"ref":"kb:404"}`),
		call("c2", "delete_all", `{}`),
		call("c3", "search", `не json`),
		{Content: "Не нашел."},
	}}
	turn, err := chat.NewService(&fakeSearch{}, m, chat.WithReader(&fakeReader{})).Answer(context.Background(), nil, "q", chat.ModeTools)
	if err != nil || turn.Answer != "Не нашел." {
		t.Fatalf("turn = %+v, %v", turn, err)
	}
	for i, want := range []string{"нет такого документа", "неизвестный инструмент", "аргументы"} {
		got := m.asked[i+1][len(m.asked[i+1])-1].Content
		if !strings.Contains(got, want) {
			t.Errorf("шаг %d: модели вернулось %q, ждали %q", i+1, got, want)
		}
	}
}

// Предел шагов: модель, что зовет инструменты без конца, получает последний
// запрос без инструментов и обязана ответить.
func TestToolsMode_StepLimit(t *testing.T) {
	var replies []chat.ModelReply
	for range chat.MaxSteps + 3 {
		replies = append(replies, call("c", "search", `{"q":"x"}`))
	}
	m := &scriptModel{replies: replies}
	_, _ = chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{})).Answer(context.Background(), nil, "q", chat.ModeTools)
	if len(m.asked) != chat.MaxSteps+1 {
		t.Fatalf("запросов к модели %d, ждали %d", len(m.asked), chat.MaxSteps+1)
	}
	if m.tools[len(m.tools)-1] != nil {
		t.Fatal("последний запрос предлагает инструменты — модель могла бы не ответить")
	}
}

// Без чтения документов модель могла отвечать по поиску: документы чата —
// найденные поиском, без повторов.
func TestToolsMode_DocsFromSearchWhenNothingRead(t *testing.T) {
	m := &scriptModel{replies: []chat.ModelReply{
		call("c1", "search", `{"q":"a"}`), call("c2", "search", `{"q":"b"}`), {Content: "ok"},
	}}
	turn, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{})).Answer(context.Background(), nil, "q", chat.ModeTools)
	if err != nil || len(turn.Docs) != 2 {
		t.Fatalf("turn = %+v, %v", turn, err)
	}
}

// Режим «модель ищет» без читателя документов — ошибка настройки, а не тихий
// переход на другой режим.
func TestToolsMode_NeedsReader(t *testing.T) {
	_, err := chat.NewService(&fakeSearch{}, &scriptModel{}).Answer(context.Background(), nil, "q", chat.ModeTools)
	if !errors.Is(err, chat.ErrNoReader) {
		t.Fatalf("err = %v", err)
	}
}

// Инструмент «запомнить» (#479): в режиме «модель ищет» модель сама
// предлагает устойчивый факт о человеке; окно кладет его на одобрение.
// Без Remember (портрет не ведется) инструмента нет вовсе.
func TestToolsMode_Remember(t *testing.T) {
	r := &fakeReader{docs: map[string]chat.Doc{}}
	m := &scriptModel{replies: []chat.ModelReply{
		call("c1", "remember", `{"section":"prefs","text":"Отвечать коротко"}`),
		{Content: "Хорошо, буду коротко."},
	}}
	var got []string
	req := chat.Request{Question: "отвечай коротко, всегда", Mode: chat.ModeTools,
		Remember: func(_ context.Context, section, text string) error { got = append(got, section+"|"+text); return nil }}
	turn, err := chat.NewService(&fakeSearch{}, m, chat.WithReader(r)).Respond(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Answer != "Хорошо, буду коротко." || len(got) != 1 || got[0] != "prefs|Отвечать коротко" {
		t.Fatalf("turn %+v, remembered %v", turn, got)
	}
	if res := m.asked[1][len(m.asked[1])-1].Content; !strings.Contains(res, "одобр") {
		t.Fatalf("модели о предложении: %q", res)
	}
	i := slices.IndexFunc(m.tools[0], func(x chat.Tool) bool { return x.Name == "remember" })
	if i < 0 {
		t.Fatal("remember не предложен модели")
	}
	for _, sec := range domain.FactSections() { // разделы — те, что знает домен
		if !strings.Contains(string(m.tools[0][i].Parameters), `"`+string(sec)+`"`) {
			t.Fatalf("раздела %q нет в remember: %s", sec, m.tools[0][i].Parameters)
		}
	}
	m2 := &scriptModel{replies: []chat.ModelReply{{Content: "ок"}}}
	req.Remember = nil
	if _, err := chat.NewService(&fakeSearch{}, m2, chat.WithReader(r)).Respond(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(m2.tools[0], func(x chat.Tool) bool { return x.Name == "remember" }) {
		t.Fatal("remember предложен без портрета")
	}
}
