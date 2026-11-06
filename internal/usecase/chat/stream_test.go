package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// streamModel streams each scripted reply by words: show получает текст,
// накопленный к этому куску.
type streamModel struct{ scriptModel }

func (m *streamModel) Stream(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool, show func(string)) (chat.ModelReply, error) {
	r, err := m.Complete(ctx, msgs, tools)
	var text strings.Builder
	for _, w := range splitWords(r.Content) {
		text.WriteString(w)
		show(text.String())
	}
	return r, err
}

func splitWords(s string) []string {
	var out []string
	start := 0
	for i, c := range s {
		if c == ' ' && i > start {
			out = append(out, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// Ответ виден по мере генерации: show получает растущий текст, а итог хода
// тот же, что без потока.
func TestAnswerStream_ShowsTextAsItComes(t *testing.T) {
	m := &streamModel{scriptModel{replies: []chat.ModelReply{{Content: "Срок — 30 дней [1]."}}}}
	var seen []string
	turn, err := chat.NewService(&fakeSearch{docs: docs}, m).
		AnswerStream(context.Background(), nil, "какой срок?", chat.ModeContext, func(s string) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 || seen[len(seen)-1] != "Срок — 30 дней [1]." || seen[0] != "Срок" {
		t.Fatalf("seen = %q", seen)
	}
	if turn.Answer != "Срок — 30 дней [1]." || len(turn.Docs) != 1 {
		t.Fatalf("turn = %+v", turn)
	}
}

// В режиме «модель ищет» текст шага, который кончился вызовом инструмента
// («сейчас поищу»), — не ответ: show получает пустую строку, и окно стирает
// его до следующего шага.
func TestAnswerStream_ToolStepClearsText(t *testing.T) {
	pre := call("c1", "search", `{"q":"срок"}`)
	pre.Content = "Сейчас поищу"
	m := &streamModel{scriptModel{replies: []chat.ModelReply{pre, {Content: "Срок 30 дней."}}}}
	var seen []string
	r := &fakeReader{docs: map[string]chat.Doc{}}
	turn, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(r)).
		AnswerStream(context.Background(), nil, "срок?", chat.ModeTools, func(s string) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Сейчас", "Сейчас поищу", "", "Срок", "Срок 30", "Срок 30 дней."}
	if len(seen) != len(want) {
		t.Fatalf("seen = %q, want %q", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("seen = %q, want %q", seen, want)
		}
	}
	if turn.Answer != "Срок 30 дней." {
		t.Fatalf("turn = %+v", turn)
	}
}

// Модель без потока отвечает как раньше: show не зовется, ответ целиком.
func TestAnswerStream_ModelWithoutStream(t *testing.T) {
	m := &fakeModel{reply: chat.ModelReply{Content: "Срок — 30 дней [1]."}}
	called := false
	turn, err := chat.NewService(&fakeSearch{docs: docs}, m).
		AnswerStream(context.Background(), nil, "срок?", chat.ModeContext, func(string) { called = true })
	if err != nil || called || turn.Answer != "Срок — 30 дней [1]." {
		t.Fatalf("turn %+v, err %v, show called %v", turn, err, called)
	}
}
