package chat_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/daniil/kb-engine/internal/usecase/versions"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

type fakeVersions struct {
	fams  []chat.VersionFamily
	texts map[string]string
	read  []string
}

func (f *fakeVersions) Families(_ context.Context, q string) ([]chat.VersionFamily, error) {
	var out []chat.VersionFamily
	for _, fam := range f.fams {
		if q == "" || strings.Contains(strings.ToLower(fam.Name), strings.ToLower(q)) {
			out = append(out, fam)
		}
	}
	return out, nil
}

func (f *fakeVersions) Text(_ context.Context, path string) (string, error) {
	f.read = append(f.read, path)
	return f.texts[path], nil
}

func kpFamily() chat.VersionFamily {
	return chat.VersionFamily{Name: "КП Ромашка", Versions: []chat.VersionInfo{
		{Path: "/p/КП_2026_11_05.docx", Date: "05.11.2026", Size: 1000, Note: "первая версия", Mark: "отправлено Алексею"},
		{Path: "/p/КП_2026_11_10.docx", Date: "10.11.2026", Size: 1100, Note: "добавлено строк: 1 — «Гарантия»", Current: true},
		{Path: "/p/КП_2026_11_12.docx", Date: "12.11.2026", Size: 1200, Note: "убрано строк: 1 — «Срок 7 дней»"},
	}}
}

func kpVersions() *fakeVersions {
	return &fakeVersions{fams: []chat.VersionFamily{kpFamily(), {Name: "Смета", Versions: []chat.VersionInfo{{Path: "/p/Смета.xlsx", Date: "01.11.2026", Note: "первая версия"}}}},
		texts: map[string]string{"/p/КП_2026_11_05.docx": "ТЕКСТ-ПЕРВОЙ", "/p/КП_2026_11_10.docx": "ТЕКСТ-ВТОРОЙ", "/p/КП_2026_11_12.docx": "ТЕКСТ-ТРЕТЬЕЙ"}}
}

// toolAnswer runs one question where the model calls a single tool and returns
// what the tool answered.
func toolAnswer(t *testing.T, v chat.Versions, name, args string) (string, chat.Turn) {
	t.Helper()
	m := &scriptModel{replies: []chat.ModelReply{call("c1", name, args), {Content: "готово"}}}
	turn, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{})).Respond(context.Background(),
		chat.Request{Question: "что с КП?", Mode: chat.ModeTools, Versions: v})
	if err != nil {
		t.Fatal(err)
	}
	last := m.asked[1][len(m.asked[1])-1]
	return last.Content, turn
}

func TestVersionsTool_OfferedOnlyWhenTheWindowHasVersions(t *testing.T) {
	for name, v := range map[string]chat.Versions{"есть": kpVersions(), "нет": nil} {
		m := &scriptModel{replies: []chat.ModelReply{{Content: "ок"}}}
		if _, err := chat.NewService(&fakeSearch{docs: docs}, m, chat.WithReader(&fakeReader{})).Respond(context.Background(),
			chat.Request{Question: "q", Mode: chat.ModeTools, Versions: v}); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range m.tools[0] {
			names = append(names, tl.Name)
		}
		has := strings.Contains(strings.Join(names, ","), "open_version") && strings.Contains(strings.Join(names, ","), "versions")
		if has != (v != nil) {
			t.Errorf("%s: инструменты %v", name, names)
		}
	}
}

func TestVersionsTool_ListsFamiliesWithNotesAndBadgeButNoFileText(t *testing.T) {
	got, _ := toolAnswer(t, kpVersions(), "versions", `{"q":"кп"}`)
	for _, want := range []string{"КП Ромашка", "05.11.2026", "первая версия", "отправлено Алексею", "актуальная", "добавлено строк: 1 — «Гарантия»", "/p/КП_2026_11_10.docx"} {
		if !strings.Contains(got, want) {
			t.Errorf("в списке версий нет %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ТЕКСТ-") || strings.Contains(got, "Смета") {
		t.Errorf("лишнее в ответе (текст файла или чужой документ):\n%s", got)
	}
}

func TestOpenVersion_PicksTheVersionByTheWordsOfThePerson(t *testing.T) {
	for _, c := range []struct{ which, want string }{
		{"последняя", "ТЕКСТ-ТРЕТЬЕЙ"},
		{"самая свежая", "ТЕКСТ-ТРЕТЬЕЙ"},
		{"предыдущая", "ТЕКСТ-ВТОРОЙ"},
		{"первая", "ТЕКСТ-ПЕРВОЙ"},
		{"v2", "ТЕКСТ-ВТОРОЙ"},
		{"версия 3", "ТЕКСТ-ТРЕТЬЕЙ"},
		{"от 05.11", "ТЕКСТ-ПЕРВОЙ"},
		{"от 10.11.2026", "ТЕКСТ-ВТОРОЙ"},
		{"актуальная", "ТЕКСТ-ВТОРОЙ"},
		{"та, что отправили Алексею", "ТЕКСТ-ПЕРВОЙ"},
		{"где убрали срок", "ТЕКСТ-ТРЕТЬЕЙ"},
	} {
		v := kpVersions()
		got, turn := toolAnswer(t, v, "open_version", fmt.Sprintf(`{"q":"кп","which":%q}`, c.which))
		if !strings.Contains(got, c.want) || len(v.read) != 1 {
			t.Errorf("«%s»: ждали %s, прочитано %v, ответ:\n%s", c.which, c.want, v.read, got)
		}
		if len(turn.Docs) != 1 || !strings.Contains(turn.Docs[0].Title, "КП Ромашка") {
			t.Errorf("«%s»: открытая версия должна стать документом хода: %+v", c.which, turn.Docs)
		}
	}
}

func TestOpenVersion_AsksInsteadOfGuessing(t *testing.T) {
	for name, c := range map[string]struct{ args, want string }{
		"нет такой версии":     {`{"q":"кп","which":"v9"}`, "такой версии нет"},
		"нет такого документа": {`{"q":"договор","which":"последняя"}`, "ничего не найдено"},
		"два документа":        {`{"q":"","which":"последняя"}`, "несколько документов"},
		"не сказано какая":     {`{"q":"кп","which":""}`, "какую именно"},
	} {
		v := kpVersions()
		got, _ := toolAnswer(t, v, "open_version", c.args)
		if !strings.Contains(got, c.want) || len(v.read) != 0 {
			t.Errorf("%s: ждали %q без чтения файла (читалось %v), ответ:\n%s", name, c.want, v.read, got)
		}
	}
}

func TestOpenVersion_LongTextIsTrimmedLikeRead(t *testing.T) {
	v := kpVersions()
	v.texts["/p/КП_2026_11_12.docx"] = strings.Repeat("я", chat.MaxReadRunes+500)
	got, _ := toolAnswer(t, v, "open_version", `{"q":"кп","which":"последняя"}`)
	if n := len([]rune(got)); n > chat.MaxReadRunes+300 {
		t.Errorf("текст версии не обрезан: %d знаков", n)
	}
}

// Сбой чтения версий уходит модели без пути и без куска текста: тот же закон,
// что у MCP, потому что ответ инструмента идет провайдеру.
func TestVersionTools_FailureIsExplainedWithoutTheText(t *testing.T) {
	leak := errors.New("обход: /Users/someone/Documents/КП.docx: SECRET-в-тексте")
	for _, c := range []struct{ tool, args string }{{"versions", `{"q":"кп"}`}, {"open_version", `{"q":"кп","which":"последняя"}`}} {
		got, _ := toolAnswer(t, &failingVersions{err: leak}, c.tool, c.args)
		if strings.Contains(got, "/Users/someone") || strings.Contains(got, "SECRET") || !strings.Contains(got, "не прочитать") {
			t.Errorf("%s: ответ модели %q", c.tool, got)
		}
	}
	got, _ := toolAnswer(t, &failingVersions{err: fmt.Errorf("версии: %w", versions.ErrNoSource)}, "versions", `{"q":"кп"}`)
	if !strings.Contains(got, "папки окна не подключены") {
		t.Errorf("причина «папки не подключены» не названа: %q", got)
	}
}

type failingVersions struct{ err error }

func (f *failingVersions) Families(context.Context, string) ([]chat.VersionFamily, error) {
	return nil, f.err
}
func (f *failingVersions) Text(context.Context, string) (string, error) { return "", f.err }
