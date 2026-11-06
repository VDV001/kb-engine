package chat_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// fakeSearch returns docs and remembers the queries.
type fakeSearch struct {
	docs    []chat.Doc
	err     error
	queries []string
}

func (f *fakeSearch) Search(_ context.Context, q string, _ int) ([]chat.Doc, error) {
	f.queries = append(f.queries, q)
	return f.docs, f.err
}

// fakeModel answers with reply and remembers what it was asked.
type fakeModel struct {
	reply chat.ModelReply
	err   error
	got   []chat.ModelMessage
}

func (f *fakeModel) Complete(_ context.Context, msgs []chat.ModelMessage, _ []chat.Tool) (chat.ModelReply, error) {
	f.got = msgs
	return f.reply, f.err
}

var docs = []chat.Doc{
	{Ref: "kb:12", Title: "Договор поставки", Source: "База", Text: "Срок поставки — 30 дней"},
	{Ref: "file:/w/tz.docx", Title: "tz.docx", Source: "Работа", Text: "Техническое задание на поставку"},
}

// Режим «окно ищет» (маленькие модели): окно находит документы само и кладет
// их в запрос с номерами; модель отвечает со ссылками [n], документы чата —
// те, на которые она сослалась.
func TestContextMode(t *testing.T) {
	s := &fakeSearch{docs: docs}
	m := &fakeModel{reply: chat.ModelReply{Content: "Срок — 30 дней [1]."}}
	svc := chat.NewService(s, m)

	turn, err := svc.Answer(context.Background(), nil, "какой срок поставки?", chat.ModeContext)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Answer != "Срок — 30 дней [1]." {
		t.Errorf("answer = %q", turn.Answer)
	}
	if len(turn.Docs) != 1 || turn.Docs[0].Ref != "kb:12" {
		t.Errorf("документы чата — процитированные: %+v", turn.Docs)
	}
	if !slices.Equal(s.queries, []string{"какой срок поставки?"}) {
		t.Errorf("queries = %v", s.queries)
	}
	var b strings.Builder
	for _, msg := range m.got {
		b.WriteString(msg.Content + "\n")
	}
	all := b.String()
	for _, want := range []string{"[1] Договор поставки", "Срок поставки — 30 дней", "[2] tz.docx", "какой срок поставки?"} {
		if !strings.Contains(all, want) {
			t.Errorf("в запросе к модели нет %q:\n%s", want, all)
		}
	}
	if m.got[0].Role != chat.RoleSystem {
		t.Errorf("первым идет системное сообщение: %+v", m.got[0])
	}
}

// Ответ без ссылок — документами чата остаются все найденные: человек должен
// видеть, на чем модель отвечала.
func TestContextMode_NoCitationsKeepsFound(t *testing.T) {
	svc := chat.NewService(&fakeSearch{docs: docs}, &fakeModel{reply: chat.ModelReply{Content: "Не знаю."}})
	turn, err := svc.Answer(context.Background(), nil, "q", chat.ModeContext)
	if err != nil || len(turn.Docs) != 2 {
		t.Fatalf("turn = %+v, %v", turn, err)
	}
}

// История идет в запрос по порядку, новый вопрос — последним.
func TestContextMode_History(t *testing.T) {
	m := &fakeModel{reply: chat.ModelReply{Content: "ok"}}
	svc := chat.NewService(&fakeSearch{}, m)
	hist := []chat.Message{{Role: chat.RoleUser, Text: "первый"}, {Role: chat.RoleAssistant, Text: "ответ"}}
	if _, err := svc.Answer(context.Background(), hist, "второй", chat.ModeContext); err != nil {
		t.Fatal(err)
	}
	var roles []chat.Role
	for _, msg := range m.got {
		roles = append(roles, msg.Role)
	}
	want := []chat.Role{chat.RoleSystem, chat.RoleUser, chat.RoleAssistant, chat.RoleUser}
	if !slices.Equal(roles, want) || !strings.Contains(m.got[3].Content, "второй") {
		t.Fatalf("roles = %v, last = %q", roles, m.got[len(m.got)-1].Content)
	}
}

// Отказы — свои ошибки, чтобы окно назвало причину: поиск и модель отдельно,
// пустой вопрос — до похода в сеть.
func TestAnswer_Errors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name  string
		s     *fakeSearch
		m     *fakeModel
		q     string
		want  error
		calls bool
	}{
		{"пустой вопрос", &fakeSearch{}, &fakeModel{}, "  ", chat.ErrEmptyQuestion, false},
		{"поиск упал", &fakeSearch{err: boom}, &fakeModel{}, "q", chat.ErrSearch, true},
		{"модель упала", &fakeSearch{docs: docs}, &fakeModel{err: boom}, "q", chat.ErrModel, true},
		{"пустой ответ модели", &fakeSearch{docs: docs}, &fakeModel{}, "q", chat.ErrModel, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := chat.NewService(tc.s, tc.m).Answer(context.Background(), nil, tc.q, chat.ModeContext)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := len(tc.s.queries) > 0; got != tc.calls {
				t.Fatalf("поиск вызван = %v, ждали %v", got, tc.calls)
			}
		})
	}
}

// Длинный документ режется: окно не отправляет модели мегабайт текста.
func TestContextMode_TrimsLongDocs(t *testing.T) {
	long := chat.Doc{Ref: "file:/w/big.md", Title: "big.md", Text: strings.Repeat("я", chat.MaxDocRunes*3)}
	m := &fakeModel{reply: chat.ModelReply{Content: "ok"}}
	if _, err := chat.NewService(&fakeSearch{docs: []chat.Doc{long}}, m).Answer(context.Background(), nil, "q", chat.ModeContext); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, msg := range m.got {
		total += len([]rune(msg.Content))
	}
	if total > chat.MaxDocRunes*2 {
		t.Fatalf("в запросе %d символов при пределе документа %d", total, chat.MaxDocRunes)
	}
}
