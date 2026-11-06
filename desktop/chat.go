package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/llm"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// chatKeyAccount — учетка ключа модели в Связке ключей окна.
const chatKeyAccount = "chat-model"

// askTimeout — сколько ждать ответ: режим «модель ищет» делает до шести
// запросов к модели подряд.
const askTimeout = 3 * time.Minute

// chatRecord is one chat as stored: файл на чат рядом с config.json, 0600.
type chatRecord struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Created  time.Time     `json:"created"`
	Updated  time.Time     `json:"updated"`
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// Docs — документы, с которыми работали в чате, без повторов.
	Docs []chatDoc `json:"docs"`
}

type chatMessage struct {
	Role string   `json:"role"`
	Text string   `json:"text"`
	Docs []string `json:"docs,omitempty"` // ref документов ответа
}

type chatDoc struct {
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Source string `json:"source,omitempty"`
}

// chatSummary is one line of the history list.
type chatSummary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

// chatSettingsView is what the page sees: без ключа, только признак.
type chatSettingsView struct {
	desktopconfig.Chat
	HasKey bool   `json:"hasKey"`
	Local  bool   `json:"local"` // модель на этом Маке: документы никуда не уходят
	Mode   string `json:"effectiveMode"`
}

// chatDesk is the window's chat: история на диске, модель из настроек,
// поиск и чтение — источники окна.
type chatDesk struct {
	settings *folderSettings
	dir      string
	search   func(ctx context.Context, q string, limit int) ([]chat.Doc, error)
	read     func(ctx context.Context, ref string) (chat.Doc, error)
	model    func(c desktopconfig.Chat, key string) chat.Model
	now      func() time.Time
	mu       sync.Mutex // один вопрос за раз: файл чата пишется целиком
}

func newChatDesk(s *folderSettings, dir string) *chatDesk {
	return &chatDesk{settings: s, dir: dir, now: time.Now,
		model: func(c desktopconfig.Chat, key string) chat.Model {
			return llm.Client{BaseURL: strings.TrimRight(c.URL, "/"), Key: key, Model: c.Model}
		}}
}

// chatMode is who finds the documents: явный выбор или по модели.
func chatMode(c desktopconfig.Chat) chat.Mode {
	switch c.Mode {
	case desktopconfig.ChatModeContext:
		return chat.ModeContext
	case desktopconfig.ChatModeTools:
		return chat.ModeTools
	}
	if c.Local() {
		return chat.ModeContext
	}
	return chat.ModeTools
}

func (d *chatDesk) config() desktopconfig.Chat {
	d.settings.mu.Lock()
	defer d.settings.mu.Unlock()
	return d.settings.cfg.Chat.WithDefaults()
}

// Settings returns the chat settings for the page.
func (d *chatDesk) Settings() chatSettingsView {
	c := d.config()
	_, err := d.settings.tokens.Get(chatKeyAccount)
	return chatSettingsView{Chat: c, HasKey: err == nil, Local: c.Local(), Mode: string(chatMode(c))}
}

// SetSettings saves the model settings; key — новый ключ, пусто — прежний.
func (d *chatDesk) SetSettings(c desktopconfig.Chat, key string) error {
	c = c.WithDefaults()
	if err := c.Check(); err != nil {
		return err
	}
	if key = strings.TrimSpace(key); key != "" {
		if err := d.settings.tokens.Set(chatKeyAccount, key); err != nil {
			return fmt.Errorf("ключ не сохранен в Связке ключей: %w", err)
		}
	}
	f := d.settings
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Chat = c
	return filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		f.publishLocked(false)
		return nil
	})
}

// Ask answers question in chat id ("" — новый чат) and saves the chat.
func (d *chatDesk) Ask(id, question string) (chatRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec := chatRecord{Created: d.now()}
	if id != "" {
		var err error
		if rec, err = d.load(id); err != nil {
			return chatRecord{}, err
		}
	}
	c := d.config()
	key, err := d.settings.tokens.Get(chatKeyAccount)
	switch {
	case err == nil:
	case !errors.Is(err, keychain.ErrNotFound):
		return chatRecord{}, humanErr{keychain.Locked, err}
	case !c.Local():
		return chatRecord{}, errors.New("нет ключа модели — введите ключ в настройках чата")
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	svc := chat.NewService(searcherFunc(d.search), d.model(c, key), chat.WithReader(readerFunc(d.read)))
	turn, err := svc.Answer(ctx, history(rec), question, chatMode(c))
	if err != nil {
		return chatRecord{}, errors.New(chatError(err))
	}
	if rec.ID == "" {
		rec.ID, rec.Title = newChatID(), title(question)
	}
	refs := make([]string, 0, len(turn.Docs))
	for _, doc := range turn.Docs {
		refs = append(refs, doc.Ref)
		if !slices.ContainsFunc(rec.Docs, func(x chatDoc) bool { return x.Ref == doc.Ref }) {
			rec.Docs = append(rec.Docs, chatDoc{Ref: doc.Ref, Title: doc.Title, Source: doc.Source})
		}
	}
	rec.Messages = append(rec.Messages,
		chatMessage{Role: string(chat.RoleUser), Text: strings.TrimSpace(question)},
		chatMessage{Role: string(chat.RoleAssistant), Text: turn.Answer, Docs: refs})
	rec.Model, rec.Updated = c.Model, d.now()
	return rec, d.save(rec)
}

// List returns the chats, newest first.
func (d *chatDesk) List() ([]chatSummary, error) {
	files, err := filepath.Glob(filepath.Join(d.dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := []chatSummary{}
	for _, f := range files {
		rec, err := d.load(strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil {
			continue // испорченный файл не прячет остальные чаты
		}
		out = append(out, chatSummary{ID: rec.ID, Title: rec.Title, Updated: rec.Updated})
	}
	slices.SortFunc(out, func(a, b chatSummary) int { return b.Updated.Compare(a.Updated) })
	return out, nil
}

// Get returns one chat.
func (d *chatDesk) Get(id string) (chatRecord, error) { return d.load(id) }

// Delete removes a chat.
func (d *chatDesk) Delete(id string) error {
	p, err := d.path(id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

var chatIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// path is the file of chat id; id приходит со страницы — только свой вид.
func (d *chatDesk) path(id string) (string, error) {
	if !chatIDRe.MatchString(id) {
		return "", fmt.Errorf("нет такого чата: %q", id)
	}
	return filepath.Join(d.dir, id+".json"), nil
}

func (d *chatDesk) load(id string) (chatRecord, error) {
	p, err := d.path(id)
	if err != nil {
		return chatRecord{}, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return chatRecord{}, err
	}
	var rec chatRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return chatRecord{}, fmt.Errorf("чат %s не читается: %w", id, err)
	}
	return rec, nil
}

// save writes the chat atomically, 0600: история — личное.
func (d *chatDesk) save(rec chatRecord) error {
	p, err := d.path(rec.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.dir, rec.ID+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после rename — уже нет
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p) // CreateTemp создает 0600
}

func history(rec chatRecord) []chat.Message {
	out := make([]chat.Message, 0, len(rec.Messages))
	for _, m := range rec.Messages {
		out = append(out, chat.Message{Role: chat.Role(m.Role), Text: m.Text})
	}
	return out
}

func newChatID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// title is the chat title: первый вопрос, до 60 символов.
func title(q string) string {
	r := []rune(strings.Join(strings.Fields(q), " "))
	if len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return string(r)
}

// chatError says in a person's words why there is no answer.
func chatError(err error) string {
	switch {
	case errors.Is(err, chat.ErrEmptyQuestion):
		return "пустой вопрос — напишите, что найти"
	case errors.Is(err, llm.ErrUnauthorized):
		return "модель не приняла ключ — проверьте ключ в настройках чата"
	case errors.Is(err, llm.ErrNoCredit):
		return "у ключа закончились кредиты — пополните баланс у провайдера"
	case errors.Is(err, llm.ErrModelNotFound):
		return "такой модели нет — проверьте имя модели в настройках чата"
	case errors.Is(err, llm.ErrInsecureURL):
		return "ключ уходит только по https или на этот Мак — поправьте адрес модели"
	case errors.Is(err, context.DeadlineExceeded):
		return "модель не ответила за 3 минуты — спросите короче или выберите модель быстрее"
	case errors.Is(err, chat.ErrSearch):
		return "поиск по источникам не удался — посмотрите панель «Проблемы»"
	case errors.Is(err, llm.ErrServer):
		return "сервер модели не ответил — проверьте адрес модели (запущен ли Ollama) и сеть"
	default:
		return "модель не ответила — " + err.Error()
	}
}

// searcherFunc and readerFunc adapt functions to the chat ports.
type searcherFunc func(ctx context.Context, q string, limit int) ([]chat.Doc, error)

func (f searcherFunc) Search(ctx context.Context, q string, limit int) ([]chat.Doc, error) {
	return f(ctx, q, limit)
}

type readerFunc func(ctx context.Context, ref string) (chat.Doc, error)

func (f readerFunc) Read(ctx context.Context, ref string) (chat.Doc, error) { return f(ctx, ref) }

// maxSeenEntries caps the entries the read tool can open: записи из выдач
// поиска текущего окна.
//
// ponytail: запись читается только из недавней выдачи (ее текст — поля
// записи). Потолок: модель не откроет запись по id, которого не видела в
// поиске. Путь: чтение записи по id у источника (каталог, GET /api/entries/{id}).
const maxSeenEntries = 500

// useSources wires the chat to the window's sources: записи — общая выдача
// (каталоги и kb-серверы), файлы — индекс папок; файл читается через индекс,
// то есть с той же проверкой, что «Открыть».
func (d *chatDesk) useSources(files *filesearch.Service) {
	var mu sync.Mutex
	seen := map[string]chat.Doc{}
	d.search = func(ctx context.Context, q string, limit int) ([]chat.Doc, error) {
		res := d.settings.SearchSources(ctx, q)
		names := map[string]string{}
		for _, r := range res.Sources {
			names[r.ID] = sourceLabel(r.Source)
		}
		var entries []chat.Doc
		for _, h := range res.Hits {
			if doc, ok := entryDoc(h, names[h.Source]); ok {
				entries = append(entries, doc)
			}
		}
		mu.Lock()
		if len(seen) > maxSeenEntries {
			clear(seen)
		}
		for _, doc := range entries {
			seen[doc.Ref] = doc
		}
		mu.Unlock()
		hits, err := files.Search(q, limit)
		if err != nil && !errors.Is(err, filesearch.ErrNotBuilt) {
			return nil, err
		}
		fileDocs := make([]chat.Doc, 0, len(hits))
		for _, h := range hits {
			fileDocs = append(fileDocs, chat.Doc{Ref: "file:" + h.Doc.Path(), Title: h.Doc.Name(), Source: filepath.Dir(h.Doc.Path()), Text: h.Snippet})
		}
		return interleave(entries, fileDocs, limit), nil
	}
	d.read = func(_ context.Context, ref string) (chat.Doc, error) {
		if p, ok := strings.CutPrefix(ref, "file:"); ok {
			text, err := files.Read(p)
			if err != nil {
				return chat.Doc{}, err
			}
			return chat.Doc{Ref: ref, Title: filepath.Base(p), Source: filepath.Dir(p), Text: text}, nil
		}
		mu.Lock()
		doc, ok := seen[ref]
		mu.Unlock()
		if !ok {
			return chat.Doc{}, fmt.Errorf("документа %q нет в выдаче поиска — найдите его сначала", ref)
		}
		return doc, nil
	}
}

// entryDoc turns one record of the merged search into a chat document.
func entryDoc(h sourceHit, source string) (chat.Doc, bool) {
	var e struct {
		ID          int      `json:"id"`
		Title       string   `json:"title"`
		URL         string   `json:"url"`
		Category    string   `json:"category"`
		Description string   `json:"description"`
		Verdict     *string  `json:"verdict"`
		Tags        []string `json:"tags"`
	}
	if err := json.Unmarshal(h.Entry, &e); err != nil || e.Title == "" {
		return chat.Doc{}, false
	}
	var b strings.Builder
	b.WriteString(e.Description)
	if e.Category != "" {
		b.WriteString("\nКатегория: " + e.Category)
	}
	if e.Verdict != nil {
		b.WriteString("\nВердикт: " + *e.Verdict)
	}
	if len(e.Tags) > 0 {
		b.WriteString("\nТеги: " + strings.Join(e.Tags, ", "))
	}
	if e.URL != "" {
		b.WriteString("\nАдрес: " + e.URL)
	}
	return chat.Doc{Ref: fmt.Sprintf("kb:%s:%d", h.Source, e.ID), Title: e.Title, Source: source, Text: strings.TrimSpace(b.String())}, true
}

// interleave alternates entries and files up to limit.
func interleave(a, b []chat.Doc, limit int) []chat.Doc {
	out := make([]chat.Doc, 0, min(limit, len(a)+len(b)))
	for i := 0; len(out) < limit && (i < len(a) || i < len(b)); i++ {
		if i < len(a) {
			out = append(out, a[i])
		}
		if i < len(b) && len(out) < limit {
			out = append(out, b[i])
		}
	}
	return out
}
