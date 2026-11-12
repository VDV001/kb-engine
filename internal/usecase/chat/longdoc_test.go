package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// Один текст отказа на все пути (ревью #581): каждый ложный обход из живой
// проверки 13.11 назван, место правки названо, без длинного тире — модель
// повторяет его человеку.
func TestLongDocument_NamesEveryDeadEnd(t *testing.T) {
	got := chat.LongDocument(chat.MaxReadRunes + 1000)
	for _, want := range []string{"вставленному в чат", "новом чате", "Word или Pages", "других обходов не предлагай", "у документа есть версия длиннее"} {
		if !strings.Contains(got, want) {
			t.Errorf("в отказе нет %q: %q", want, got)
		}
	}
	if !strings.Contains(got, chat.LongDocAdvice) || strings.Contains(got, "—") {
		t.Errorf("отказ не из общего совета или с длинным тире: %q", got)
	}
}

// Неполное чтение — словами на обоих путях чтения: open_version и read.
func TestPartialRead_SaysSoOnBothPaths(t *testing.T) {
	v := kpVersions()
	v.texts["/p/КП_2026_11_12.docx"] = strings.Repeat("я", chat.MaxReadRunes+10)
	got, _ := toolAnswer(t, v, "open_version", `{"q":"кп","which":"последняя"}`)
	if !strings.Contains(got, chat.LongDocAdvice) || chat.LongDocAdvice == "" {
		t.Errorf("open_version: нет общего совета: %q", got[len(got)-200:])
	}
	long := chat.Doc{Ref: "file:/d/a.docx", Title: "Договор", Text: strings.Repeat("я", chat.MaxReadRunes+10)}
	m := &scriptModel{replies: []chat.ModelReply{call("c1", "read", `{"ref":"file:/d/a.docx"}`), {Content: "готово"}}}
	if _, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{docs: map[string]chat.Doc{long.Ref: long}})).Respond(context.Background(),
		chat.Request{Question: "q", Mode: chat.ModeTools}); err != nil {
		t.Fatal(err)
	}
	if r := m.asked[1][len(m.asked[1])-1].Content; !strings.Contains(r, "прочитан не целиком") {
		t.Errorf("read: обрезка без слов: %q", r[len(r)-120:])
	}
}

// Предел чтения — в правилах для модели заранее, а не только в отказе после работы.
func TestProposeVersionPrompt_NamesTheReadLimit(t *testing.T) {
	m := &scriptModel{replies: []chat.ModelReply{{Content: "ок"}}}
	if _, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{})).Respond(context.Background(),
		chat.Request{Question: "q", Mode: chat.ModeTools, Versions: kpVersions(), ProposeVersion: func(context.Context, chat.VersionProposal) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if sys := m.asked[0][0].Content; !strings.Contains(sys, "8000 знаков") {
		t.Errorf("в правилах нет предела чтения:\n%s", sys)
	}
}
