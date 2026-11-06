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
	c, err := d.Ask("", "какой срок поставки?")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Title != "какой срок поставки?" || len(c.Messages) != 2 || c.Messages[1].Text != "ответ [1]" {
		t.Fatalf("chat = %+v", c)
	}
	c2, err := d.Ask(c.ID, "а штраф?")
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
	if _, err := d.Ask("", "q"); err == nil || !strings.Contains(err.Error(), "ключ") || m.n != 0 {
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
		_, err := d.Ask("", "q")
		if err == nil || !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), " — ") {
			t.Errorf("%v: err = %v, want «%s» и действие", tc.err, err, tc.says)
		}
	}
}

// Удаленный чат пропадает из списка.
func TestChat_Delete(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	_ = d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "x"}, "")
	c, err := d.Ask("", "q")
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
