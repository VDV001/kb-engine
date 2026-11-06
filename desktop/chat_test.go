package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/llm"
	"github.com/daniil/kb-engine/internal/adapter/weblink"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// echoModel answers «ответ N [1]» and remembers what it was asked.
type echoModel struct {
	n     int
	asked [][]chat.ModelMessage
	err   error
}

func (m *echoModel) Complete(_ context.Context, msgs []chat.ModelMessage, _ []chat.Tool) (chat.ModelReply, error) {
	m.n++
	m.asked = append(m.asked, msgs)
	if m.err != nil {
		return chat.ModelReply{}, m.err
	}
	return chat.ModelReply{Content: "ответ [1]"}, nil
}

// newTestChatDesk wires a chat over a fake search and model; settings — как в
// тестах источников, Связка — в памяти.
func newTestChatDesk(t *testing.T) (*chatDesk, *echoModel, *fakeTokens, *desktopconfig.Chat) {
	t.Helper()
	s, tok, _ := newServerSettings(t)
	m := &echoModel{}
	var used desktopconfig.Chat
	d := newChatDesk(s, filepath.Join(filepath.Dir(s.path), "chats"))
	d.search = func(_ context.Context, q string, _ int) ([]chat.Doc, error) {
		return []chat.Doc{{Ref: "kb:base:" + q, Title: "Договор " + q, Source: "База", Text: "срок 30 дней"}}, nil
	}
	d.read = func(context.Context, string) (chat.Doc, error) { return chat.Doc{}, errors.New("нет") }
	d.model = func(c desktopconfig.Chat, _ string) chat.Model { used = c; return m }
	d.now = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }
	return d, m, tok, &used
}

// Первый вопрос заводит чат с заголовком из вопроса, второй продолжает его:
// модель видит прежний разговор, документы чата копятся без повторов, файл —
// только владельцу (0600).
func TestChat_AskCreatesAndContinues(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	c, err := d.Ask(askReq{ID: "", Question: "какой срок поставки?"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Title != "какой срок поставки?" || len(c.Messages) != 2 || c.Messages[1].Text != "ответ [1]" {
		t.Fatalf("chat = %+v", c)
	}
	c2, err := d.Ask(askReq{ID: c.ID, Question: "а штраф?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Messages) != 4 || len(c2.Docs) != 2 {
		t.Fatalf("второй вопрос: %d сообщений, %d документов", len(c2.Messages), len(c2.Docs))
	}
	last := m.asked[1]
	if !strings.Contains(last[1].Content, "какой срок поставки?") || last[2].Content != "ответ [1]" {
		t.Fatalf("модель не видела прежний разговор: %+v", last)
	}
	info, err := os.Stat(filepath.Join(d.dir, c.ID+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("файл чата: %v, %v", info, err)
	}
	list, err := d.List()
	if err != nil || len(list) != 1 || list[0].ID != c.ID || list[0].Title != c.Title {
		t.Fatalf("list = %+v, %v", list, err)
	}
	got, err := d.Get(c.ID)
	if err != nil || len(got.Messages) != 4 {
		t.Fatalf("get = %+v, %v", got, err)
	}
}

// Ключ модели — только в Связке ключей: в config.json его нет, окну отдается
// лишь признак «ключ есть»; пустой ключ при сохранении не стирает прежний.
func TestChat_KeyOnlyInKeychain(t *testing.T) {
	d, _, tok, _ := newTestChatDesk(t)
	const secret = "gw-not-a-real-key"
	if err := d.SetSettings(desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Model: "anthropic/claude-sonnet-5.5"}, secret); err != nil {
		t.Fatal(err)
	}
	if tok.m[chatKeyAccount] != secret {
		t.Fatalf("ключ не в Связке: %v", tok.m)
	}
	raw, _ := os.ReadFile(d.settings.path)
	if strings.Contains(string(raw), secret) {
		t.Fatal("ключ попал в config.json")
	}
	if err := d.SetSettings(desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Model: "anthropic/claude-haiku-5.5"}, ""); err != nil {
		t.Fatal(err)
	}
	v := d.Settings()
	if !v.HasKey || v.Model != "anthropic/claude-haiku-5.5" || tok.m[chatKeyAccount] != secret {
		t.Fatalf("view = %+v, keychain = %v", v, tok.m)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), secret) {
		t.Fatal("ключ ушел на страницу")
	}
}

// Режим по модели (решение владельца 06.11): локальная — окно ищет, облачная
// — модель ищет; явный выбор в настройках главнее.
func TestChat_Mode(t *testing.T) {
	tests := []struct {
		c    desktopconfig.Chat
		want chat.Mode
	}{
		{desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1"}, chat.ModeContext},
		{desktopconfig.Chat{URL: "http://localhost:11434/v1"}, chat.ModeContext},
		{desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1"}, chat.ModeTools},
		{desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Mode: "context"}, chat.ModeContext},
		{desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Mode: "tools"}, chat.ModeTools},
	}
	for _, tc := range tests {
		if got := chatMode(tc.c); got != tc.want {
			t.Errorf("%+v: mode %q, want %q", tc.c, got, tc.want)
		}
	}
}

// Облачная модель без ключа — понятный отказ до похода в сеть; адрес http не
// на этот Мак — отказ при сохранении (ключ ушел бы открытым).
func TestChat_Refusals(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Model: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ask(askReq{ID: "", Question: "q"}); err == nil || !strings.Contains(err.Error(), "ключ") || m.n != 0 {
		t.Fatalf("без ключа: err = %v, model calls %d", err, m.n)
	}
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://192.0.2.10:11434/v1", Model: "x"}, "k"); err == nil {
		t.Fatal("http не на этот Мак принят")
	}
	for _, id := range []string{"../config", "a/b", ".."} {
		if _, err := d.Get(id); err == nil {
			t.Errorf("Get(%q) принят", id)
		}
	}
}

// Отказ модели — словами человека, с действием.
func TestChat_ModelErrorsInWords(t *testing.T) {
	tests := []struct {
		err  error
		says string
	}{
		{llm.ErrUnauthorized, "ключ"},
		{llm.ErrNoCredit, "кредит"},
		{llm.ErrModelNotFound, "имя модели"},
		{llm.ErrServer, "адрес"},
		{context.DeadlineExceeded, "не ответила"},
	}
	for _, tc := range tests {
		d, m, _, _ := newTestChatDesk(t)
		_ = d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "x"}, "")
		m.err = tc.err
		_, err := d.Ask(askReq{ID: "", Question: "q"})
		if err == nil || !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), " — ") {
			t.Errorf("%v: err = %v, want «%s» и действие", tc.err, err, tc.says)
		}
	}
}

// Удаленный чат пропадает из списка.
func TestChat_Delete(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	_ = d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "x"}, "")
	c, err := d.Ask(askReq{ID: "", Question: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.List(); len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
}

// Запись из общей выдачи становится документом чата: заголовок, источник по
// имени из списка и текст из полей записи; ref ведет к источнику и id.
func TestEntryDoc(t *testing.T) {
	raw := json.RawMessage(`{"id":12,"title":"Договор поставки","url":"https://example.org/d","category":"management",
		"description":"Срок поставки 30 дней","verdict":"keep","tags":["договор","поставка"]}`)
	d, ok := entryDoc(sourceHit{Source: "catalog-1", Entry: raw}, "База")
	if !ok || d.Ref != "kb:catalog-1:12" || d.Title != "Договор поставки" || d.Source != "База" {
		t.Fatalf("doc = %+v, %v", d, ok)
	}
	for _, want := range []string{"Срок поставки 30 дней", "https://example.org/d", "keep", "договор"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("в тексте нет %q: %q", want, d.Text)
		}
	}
	if _, ok := entryDoc(sourceHit{Source: "x", Entry: json.RawMessage(`not json`)}, ""); ok {
		t.Fatal("битая запись принята")
	}
}

// Чат ищет и в файлах папок, читает файл через индекс (та же проверка, что у
// «Открыть»); запись читается только из выдачи поиска, файл вне папок — нет.
func TestChatSources_FilesAndRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "tz.md"), "Техническое задание: срок поставки 45 дней")
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "", extraction{})
	if err != nil {
		t.Fatal(err)
	}
	x.start(t.Context())
	waitIdle(t, x)
	d, _, _, _ := newTestChatDesk(t)
	d.useSources(x.files)

	docs, err := d.search(context.Background(), "поставки", 8)
	if err != nil {
		t.Fatal(err)
	}
	var ref string
	for _, doc := range docs {
		if doc.Title == "tz.md" {
			ref = doc.Ref
		}
	}
	if ref == "" {
		t.Fatalf("файл не найден: %+v", docs)
	}
	got, err := d.read(context.Background(), ref)
	if err != nil || !strings.Contains(got.Text, "45 дней") {
		t.Fatalf("read = %+v, %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	writeFile(t, outside, "секрет")
	for _, bad := range []string{"file:" + outside, "kb:catalog-1:999", "что-то"} {
		if _, err := d.read(context.Background(), bad); err == nil {
			t.Errorf("read(%q) принят", bad)
		}
	}
}

// Записи и файлы чередуются: ни одна сторона не вытесняет другую целиком.
func TestInterleaveDocs(t *testing.T) {
	e := []chat.Doc{{Ref: "e1"}, {Ref: "e2"}, {Ref: "e3"}}
	f := []chat.Doc{{Ref: "f1"}}
	var refs []string
	for _, d := range interleave(e, f, 3) {
		refs = append(refs, d.Ref)
	}
	if strings.Join(refs, ",") != "e1,f1,e2" {
		t.Fatalf("refs = %v", refs)
	}
}

// streamEcho is echoModel that streams its answer in two pieces.
type streamEcho struct{ echoModel }

func (m *streamEcho) Stream(ctx context.Context, msgs []chat.ModelMessage, tools []chat.Tool, show func(string)) (chat.ModelReply, error) {
	r, err := m.Complete(ctx, msgs, tools)
	show("ответ")
	show(r.Content)
	return r, err
}

// Ответ виден по мере генерации: окно получает растущий текст событием, а
// сохраняет и возвращает весь ответ, как без потока.
func TestChat_AskShowsAnswerAsItComes(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	m := &streamEcho{}
	d.model = func(desktopconfig.Chat, string) chat.Model { return m }
	var seen []string
	d.show = func(s string) { seen = append(seen, s) }
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	c, err := d.Ask(askReq{ID: "", Question: "срок?"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, "|") != "ответ|ответ [1]" || c.Messages[1].Text != "ответ [1]" {
		t.Fatalf("seen %q, chat %+v", seen, c)
	}
}

// Приложенное к вопросу и ссылки из его текста читаются окном и идут модели
// первыми; в документах чата остаются адрес и название, текст — нет.
func TestChat_AskWithAttachments(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	var asked []string
	d.attach = func(_ context.Context, ref string) (chat.Doc, error) {
		asked = append(asked, ref)
		return chat.Doc{Ref: ref, Title: "по " + ref, Source: "example.com", Text: "приложенный текст " + ref}, nil
	}
	c, err := d.Ask(askReq{Question: "что тут? https://example.com/tz.", Refs: []string{"file:/w/смета.docx"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, " ") != "file:/w/смета.docx url:https://example.com/tz" {
		t.Fatalf("прочитано: %q", asked)
	}
	q := m.asked[0][len(m.asked[0])-1].Content
	if !strings.Contains(q, "приложенный текст file:/w/смета.docx") || !strings.Contains(q, "приложенный текст url:https://example.com/tz") {
		t.Fatalf("модель не получила приложенное:\n%s", q)
	}
	if len(c.Docs) < 2 || c.Docs[0].Ref != "file:/w/смета.docx" || c.Docs[1].Ref != "url:https://example.com/tz" {
		t.Fatalf("docs = %+v", c.Docs)
	}
	raw, _ := os.ReadFile(filepath.Join(d.dir, c.ID+".json"))
	if strings.Contains(string(raw), "приложенный текст") {
		t.Fatal("текст приложенного попал в файл чата")
	}
}

// Не прочитанное приложенное — вопрос не уходит, человек видит, что и почему.
func TestChat_AskAttachmentRefused(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	d.attach = func(context.Context, string) (chat.Doc, error) { return chat.Doc{}, weblink.ErrClosed }
	// Приложенное «+» не прочлось — вопрос не уходит, причина словами.
	_, err := d.Ask(askReq{Question: "глянь", Refs: []string{"url:https://docs.google.com/document/d/1A/edit"}})
	if err == nil || !strings.Contains(err.Error(), "закрыт") || !strings.Contains(err.Error(), "docs.google.com") || m.n != 0 {
		t.Fatalf("err = %v, модель звали %d раз", err, m.n)
	}
	// Та же ссылка в тексте вопроса — вопрос уходит, ссылка названа в notes.
	c, err := d.Ask(askReq{Question: "глянь https://docs.google.com/document/d/1A/edit"})
	if err != nil || m.n != 1 || len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "закрыт") {
		t.Fatalf("err = %v, notes %q, модель %d", err, c.Notes, m.n)
	}
}

// Документ из файла вне папок окна читается с диска: текст как есть, docx —
// его текстом; не текст — отказ, а не мусор модели.
func TestReadDiskFile(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "заметка.md")
	_ = os.WriteFile(txt, []byte("# Срок\n30 дней"), 0o600)
	bin := filepath.Join(dir, "a.bin")
	_ = os.WriteFile(bin, []byte{0xff, 0xfe, 0x00}, 0o600)
	doc, err := readDiskFile(context.Background(), extraction{}, txt)
	if err != nil || doc.Text != "# Срок\n30 дней" || doc.Title != "заметка.md" || doc.Ref != "file:"+txt {
		t.Fatalf("md: %+v, %v", doc, err)
	}
	if _, err := readDiskFile(context.Background(), extraction{}, bin); err == nil {
		t.Fatal("двоичный файл прочитан как текст")
	}
	if _, err := readDiskFile(context.Background(), extraction{}, filepath.Join(dir, "a.pdf")); err == nil {
		t.Fatal("PDF без помощника распознавания прочитан")
	}
}

// Ряд источников над полем (решение владельца 06.11): чат ищет только в
// отмеченных для него источниках, список хранится в чате; у документа — id
// источника, по нему окно красит значок.
func TestChat_AskInChosenSources(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	d.search = func(context.Context, string, int) ([]chat.Doc, error) {
		return []chat.Doc{
			{Ref: "kb:base:1", Title: "Договор", SourceID: "base", Text: "срок 30 дней"},
			{Ref: "file:/w/tz.md", Title: "tz.md", SourceID: "work", Text: "срок 45 дней"},
		}, nil
	}
	c, err := d.Ask(askReq{Question: "срок?", Sources: []string{"work"}})
	if err != nil {
		t.Fatal(err)
	}
	q := m.asked[0][len(m.asked[0])-1].Content
	if strings.Contains(q, "Договор") || !strings.Contains(q, "tz.md") {
		t.Fatalf("модель получила не те документы:\n%s", q)
	}
	if strings.Join(c.Sources, ",") != "work" || len(c.Docs) != 1 || c.Docs[0].Src != "work" {
		t.Fatalf("chat = %+v", c)
	}
	// Без списка — все источники, как раньше.
	if _, err := d.Ask(askReq{ID: c.ID, Question: "а штраф?", Sources: nil}); err != nil {
		t.Fatal(err)
	}
	if q := m.asked[1][len(m.asked[1])-1].Content; !strings.Contains(q, "Договор") {
		t.Fatalf("без списка искал не везде:\n%s", q)
	}
}

// Файл принадлежит папке-источнику, в которой лежит; вложенные папки — самой
// глубокой, соседняя с похожим именем — не своя.
func TestFolderSourceOf(t *testing.T) {
	srcs := []desktopconfig.Source{
		{ID: "work", Kind: desktopconfig.KindFolder, Path: "/w"},
		{ID: "deep", Kind: desktopconfig.KindFolder, Path: "/w/tenders"},
		{ID: "kb", Kind: desktopconfig.KindCatalog, Path: "/w/kb/catalog.json"},
	}
	tests := map[string]string{"/w/a.md": "work", "/w/tenders/t.docx": "deep", "/work/x.md": "", "/elsewhere/a.md": ""}
	for p, want := range tests {
		if got := folderSourceOf(p, srcs, "/home"); got != want {
			t.Errorf("folderSourceOf(%q) = %q, want %q", p, got, want)
		}
	}
}

// Ссылка, просто упомянутая в вопросе, не останавливает его, если не
// прочлась: вопрос уходит, человек видит, какую ссылку окно не прочло.
// Приложенное «+» по-прежнему останавливает (ревью #464).
func TestChat_AutoLinkFailureDoesNotBlock(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	d.attach = func(_ context.Context, ref string) (chat.Doc, error) {
		if strings.Contains(ref, "localhost") {
			return chat.Doc{}, weblink.ErrServer
		}
		return chat.Doc{Ref: ref, Title: "ок", Text: "текст"}, nil
	}
	c, err := d.Ask(askReq{Question: "почему https://localhost:8080/api не отвечает?"})
	if err != nil || m.n != 1 {
		t.Fatalf("вопрос остановлен: %v, модель звали %d", err, m.n)
	}
	if len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "localhost:8080") {
		t.Fatalf("notes = %q", c.Notes)
	}
	if _, err := d.Ask(askReq{Question: "что тут?", Refs: []string{"url:https://localhost:8080/x"}}); err == nil {
		t.Fatal("приложенное «+» не прочлось, а вопрос ушел")
	}
}

// Найденное через «+ Из найденного» читается при отправке, даже если
// следующие поиски переполнили выдачу чата (ревью #464).
func TestChat_FoundAttachmentSurvivesSearches(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	d.rememberFound([]chat.Doc{{Ref: "kb:base:7", Title: "Договор", Text: "срок 30 дней"}})
	doc, ok := d.foundDoc("kb:base:7")
	if !ok || doc.Text != "срок 30 дней" {
		t.Fatalf("doc = %+v, %v", doc, ok)
	}
}

// Кусков сотни, а страница перерисовывает весь текст на каждом: показ
// прореживается до одного раза в промежуток. Пустой (показанное — не ответ)
// проходит всегда, пропущенный хвост приходит целым ответом из Ask.
func TestEveryAtMost(t *testing.T) {
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return at }
	var seen []string
	show := everyAtMost(50*time.Millisecond, now, func(s string) { seen = append(seen, s) })
	steps := []struct {
		after time.Duration
		text  string
	}{
		{0, "о"},
		{10 * time.Millisecond, "от"}, // рано — пропуск
		{50 * time.Millisecond, "отв"},
		{10 * time.Millisecond, ""}, // пустой — всегда
		{60 * time.Millisecond, "ответ"},
	}
	for _, s := range steps {
		at = at.Add(s.after)
		show(s.text)
	}
	if got := strings.Join(seen, "|"); got != "о|отв||ответ" {
		t.Fatalf("seen %q", got)
	}
}

// Облачный голос берет ключ модели чата, только когда чат идет через
// Vercel AI Gateway: ключ другого провайдера на шлюз не уходит.
func TestChat_GatewayKey(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	const secret = "gw-not-a-real-key"
	if err := d.SetSettings(desktopconfig.Chat{URL: "https://api.openai.com/v1", Model: "gpt-5"}, secret); err != nil {
		t.Fatal(err)
	}
	if k, err := d.gatewayKey(); err == nil || k != "" {
		t.Fatalf("ключ OpenAI отдан шлюзу: %q", k)
	}
	if err := d.SetSettings(desktopconfig.Chat{URL: "https://ai-gateway.vercel.sh/v1", Model: "m/m"}, ""); err != nil {
		t.Fatal(err)
	}
	if k, err := d.gatewayKey(); err != nil || k != secret {
		t.Fatalf("ключ шлюза = %q, %v", k, err)
	}
}

// Акцент на Маке — английский голос нужного произношения из списка say;
// звуковые эффекты (Bells, Bad News) голосом не считаются.
func TestEnglishVoice(t *testing.T) {
	out := "Bad News            en_US    # Hello\nBells               en_US    # Hello\nSamantha            en_US    # Hello\nDaniel              en_GB    # Hello\n"
	if v := englishVoiceIn(out, "en-US"); v != "Samantha" {
		t.Fatalf("en-US = %q", v)
	}
	if v := englishVoiceIn(out, "en-GB"); v != "Daniel" {
		t.Fatalf("en-GB = %q", v)
	}
	if v := englishVoiceIn("Bells               en_US    # Hello\n", "en-US"); v != "" {
		t.Fatalf("эффект выбран голосом: %q", v)
	}
}
