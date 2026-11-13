package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/adapter/usagejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
	"github.com/daniil/kb-engine/internal/usecase/portrait"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// chatKeyAccount — учетка ключа модели в Связке ключей окна.
const chatKeyAccount = "chat-model"

// askTimeout — сколько ждать ответ: режим «модель ищет» делает до шести
// запросов к модели подряд.
const askTimeout = 3 * time.Minute

// Поиск для «+ Из найденного»: короткий, это подбор, а не ответ.
const (
	findTimeout = 15 * time.Second
	findLimit   = 20
)

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
	// Sources — источники, в которых ищет этот чат (ряд над полем); пусто — все.
	Sources []string `json:"sources,omitempty"`
	// Notes — что окно не смогло прочитать из ссылок вопроса; только в ответе.
	Notes []string `json:"notes,omitempty"`
	// Portrait — пошел ли портрет в чат (#479); только в ответе.
	Portrait *portraitNote `json:"portrait,omitempty"`
	// ContextUsed — токены запроса по счету провайдера в последнем ответе; в
	// файле, чтобы кольцо контекста было и у открытого позже чата (0 — не знаем).
	ContextUsed int `json:"contextUsed,omitempty"`
	// Context — заполнение контекста модели; собирается из ContextUsed и
	// размера окна из текущих настроек, в файл не пишется.
	Context *contextNote `json:"context,omitempty"`
	// Compressed — конспект начала разговора (#539); Messages остаются целыми.
	Compressed *chatCompressed `json:"compressed,omitempty"`
	// CanCompress — есть ли что сжимать; только в ответе, в файл не пишется.
	CanCompress bool `json:"canCompress,omitempty"`
}

// chatCompressed is the summary that stands for the first Folded messages
// when the model is asked.
type chatCompressed struct {
	Text   string `json:"text"`
	Folded int    `json:"folded"`
}

// contextNote is how full the model's context is: токены запроса по счету
// провайдера и размер контекста из настроек (0 — не задан). Решение владельца
// 09.11: около половины контекста качество еще то же, дальше пора записать
// handoff и начать новый чат; порог выбирает окно, а не провайдер.
type contextNote struct {
	Used   int `json:"used"`
	Window int `json:"window"`
}

// askReq is one question from the page: чат ("" — новый), вопрос, приложенное
// (file:…, url:…, найденное) и источники этого чата (пусто — все).
type askReq struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Refs     []string `json:"refs"`
	Sources  []string `json:"sources"`
	// Portrait — как портрет идет в чат, когда не помещается: "", full, relevant.
	Portrait string `json:"portrait"`
}

type chatMessage struct {
	Role string   `json:"role"`
	Text string   `json:"text"`
	Docs []string `json:"docs,omitempty"` // ref документов ответа
	// Uses — откуда ответ: документы, подключения, внешнее (отзыв 14.11).
	Uses []chat.Use `json:"uses,omitempty"`
}

type chatDoc struct {
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Source string `json:"source,omitempty"`
	Src    string `json:"src,omitempty"` // id источника окна — цвет значка
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
	// show shows the answer while the model writes it (событие chat:delta);
	// nil — без показа. Вопрос один за раз, поэтому текст без id чата.
	show func(text string)
	// step shows where the turn goes (событие chat:step); nil — без показа.
	step func(chat.Use)
	// attach reads one attached document by ref (attach.go); nil — только в окне.
	attach func(ctx context.Context, ref string) (chat.Doc, error)
	// versions — итерации документов для инструментов versions и open_version
	// режима «модель ищет»; nil — инструментов нет (тесты без вкладки «Артефакты»).
	versions *versionsDesk
	// versionNew tells the page about a new iteration offered for approval
	// (событие versions:proposal); proposals ждут подтверждения в памяти.
	versionNew func(iterationView)
	pmu        sync.Mutex
	proposals  []pendingIteration
	mu         sync.Mutex // один вопрос за раз: файл чата пишется целиком
	// memoryNew tells the page about new facts waiting for approval (событие portrait:new).
	memoryNew  func(portraitNews)
	portraitMu sync.Mutex

	foundMu sync.Mutex
	found   map[string]chat.Doc // предложенное «+ Из найденного»
	seenMu  sync.Mutex
	seen    map[string]chat.Doc // записи из поисков чата: модель читает только их
	// driveRead reads a link through Google connection conn (attach.go).
	driveRead func(ctx context.Context, conn, ref string) (chat.Doc, error)
	// calls keeps every model call for the window statistics; nil — не считать.
	calls callLog
	// prices gives the gateway's model prices for «Деньги» (#493); nil — без цен шлюза.
	prices priceList
}

// everyAtMost passes text to show at most once per gap: кусков ответа сотни,
// а страница перерисовывает весь текст на каждом. Пустой (показанное — не
// ответ) проходит всегда; пропущенный хвост приходит целым ответом из Ask.
// Вопрос один за раз, поэтому без замка.
func everyAtMost(gap time.Duration, now func() time.Time, show func(string)) func(string) {
	var last time.Time
	return func(text string) {
		if t := now(); text == "" || t.Sub(last) >= gap {
			last = t
			show(text)
		}
	}
}

func newChatDesk(s *folderSettings, dir string) *chatDesk {
	return &chatDesk{settings: s, dir: dir, now: time.Now,
		model: func(c desktopconfig.Chat, key string) chat.Model {
			return llm.Client{BaseURL: strings.TrimRight(c.URL, "/"), Key: key, Model: c.Model}
		},
		calls: usagejsonl.Journal{Path: filepath.Join(filepath.Dir(dir), "usage.jsonl")}, prices: gatewayPriceList(dir)}
}

// metered counts the calls of m for the window statistics: назначение, токены,
// время и чат (chatID, пусто — вызов вне чата); без журнала — m как есть. Сбой
// записи не мешает ответу.
func (d *chatDesk) metered(m chat.Model, c desktopconfig.Chat, p usage.Purpose, chatID string) chat.Model {
	if d.calls == nil {
		return m
	}
	return usage.NewMeter(m, c.Model, p, d.calls, d.now, func(err error) { log.Printf("статистика: %v", err) }, usage.WithChat(chatID))
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

// modelKey returns the chat model key; у модели на этом Маке ключа может не быть.
func (d *chatDesk) modelKey(c desktopconfig.Chat) (string, error) {
	key, err := d.settings.tokens.Get(chatKeyAccount)
	switch {
	case err == nil, errors.Is(err, keychain.ErrNotFound) && c.Local():
		return key, nil
	case !errors.Is(err, keychain.ErrNotFound):
		return "", humanErr{keychain.Locked, err}
	}
	return "", errors.New("нет ключа модели — введите ключ в настройках чата")
}

// gatewayKey returns the chat key when the chat goes through Vercel AI
// Gateway — облачные голоса и распознавание берут его же (#478).
func (d *chatDesk) gatewayKey() (string, error) { return d.settings.gatewayKey() }

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

// Ask answers r.Question in chat r.ID ("" — новый чат) with the attached refs
// and the links of the question, searching only r.Sources when given, and
// saves the chat.
func (d *chatDesk) Ask(r askReq) (chatRecord, error) {
	id, question, refs := r.ID, r.Question, r.Refs
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
	key, err := d.modelKey(c)
	if err != nil {
		return chatRecord{}, err
	}
	switch r.Portrait {
	case "", string(portrait.ContextFull), string(portrait.ContextRelevant):
	default:
		return chatRecord{}, fmt.Errorf("портрет: режим %q", r.Portrait)
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	about, pnote := d.portraitFor(ctx, c, question, r.Portrait)
	// id — до ответа: «запомнить» во время ответа и разбор после него
	// называют странице свой чат, даже новый.
	if rec.ID == "" {
		rec.ID, rec.Title = newChatID(), title(question)
	}
	explicit, auto := attachRefs(refs, question)
	web := d.webSteps(append(slices.Clone(explicit), auto...))
	attached, err := d.readAttached(ctx, explicit)
	if err != nil {
		return chatRecord{}, err
	}
	linked, notes := d.readQuestionLinks(ctx, auto, maxAttached-len(attached))
	attached = append(attached, linked...)
	model := d.model(c, key)
	svc := chat.NewService(searcherFunc(scoped(d.search, r.Sources)), d.metered(model, c, usage.AnswerOrSearch, rec.ID), chat.WithReader(readerFunc(d.read)))
	turn, err := svc.Respond(ctx, chat.Request{History: history(rec), Question: question, Mode: chatMode(c), Attached: attached, Show: d.show, About: about, Remember: d.rememberFor(c, rec.ID), Versions: d.versionsAccess(), ProposeVersion: d.proposeVersionFor(rec.ID),
		Extras: shopExtras(d.settings, ozon.DefaultURL, nil, time.Now, r.Sources), Step: d.step})
	if err != nil {
		return chatRecord{}, errors.New(chatError(err))
	}
	used := make([]string, 0, len(turn.Docs))
	for _, doc := range turn.Docs {
		used = append(used, doc.Ref)
		if !slices.ContainsFunc(rec.Docs, func(x chatDoc) bool { return x.Ref == doc.Ref }) {
			rec.Docs = append(rec.Docs, chatDoc{Ref: doc.Ref, Title: doc.Title, Source: doc.Source, Src: doc.SourceID})
		}
	}
	rec.Messages = append(rec.Messages,
		chatMessage{Role: string(chat.RoleUser), Text: strings.TrimSpace(question)},
		chatMessage{Role: string(chat.RoleAssistant), Text: turn.Answer, Docs: used, Uses: append(web, turn.Uses...)})
	rec.Model, rec.Updated, rec.Sources = c.Model, d.now(), slices.Clone(r.Sources)
	rec.ContextUsed = turn.PromptTokens // 0 — провайдер не прислал счет: старое число не показываем
	if err := d.save(rec); err != nil {
		return chatRecord{}, err
	}
	// Вопрос ушел: черновик и этого чата, и «нового» (из него чат родился) не нужен.
	_ = d.DropDraft("")
	_ = d.DropDraft(rec.ID)
	rec.Notes = notes // только в ответ странице: в файл чата не пишутся
	rec.Portrait = pnote
	rec.CanCompress = canCompress(rec)
	rec.Context = d.contextOf(rec)
	d.extractAfter(c, model, rec.ID, question, turn.Answer)
	return rec, nil
}

// Find searches for documents to attach («+ Из найденного»): то же, что ищет
// чат, поэтому найденное потом читается по ref.
func (d *chatDesk) Find(q string) ([]chatDoc, error) {
	q = strings.TrimSpace(q)
	if q == "" || d.search == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()
	docs, err := d.search(ctx, q, findLimit)
	if err != nil {
		return nil, err
	}
	d.rememberFound(docs)
	out := make([]chatDoc, 0, len(docs))
	for _, doc := range docs {
		out = append(out, chatDoc{Ref: doc.Ref, Title: doc.Title, Source: doc.Source})
	}
	return out, nil
}

// scoped narrows search to the chat's sources; пусто — все.
//
// ponytail: отбор после поиска — отмеченных документов может прийти меньше
// limit. Потолок: чат на один маленький источник среди больших. Путь: искать
// с запасом (limit×3) или передавать список в сам поиск источников.
func scoped(search func(context.Context, string, int) ([]chat.Doc, error), only []string) func(context.Context, string, int) ([]chat.Doc, error) {
	if len(only) == 0 {
		return search
	}
	return func(ctx context.Context, q string, limit int) ([]chat.Doc, error) {
		docs, err := search(ctx, q, limit)
		return slices.DeleteFunc(docs, func(d chat.Doc) bool { return !slices.Contains(only, d.SourceID) }), err
	}
}

// folderSourceOf returns the folder source a file lives in: самая глубокая из
// содержащих его папок; "" — файл вне папок окна.
func folderSourceOf(path string, sources []desktopconfig.Source, home string) string {
	best, bestLen := "", -1
	for _, s := range sources {
		if s.Kind != desktopconfig.KindFolder {
			continue
		}
		root := filepath.Clean(desktopconfig.ExpandHome(s.Path, home))
		if (path == root || strings.HasPrefix(path, root+string(filepath.Separator))) && len(root) > bestLen {
			best, bestLen = s.ID, len(root)
		}
	}
	return best
}

// maxFound caps what «+ Из найденного» remembers for attaching.
const maxFound = 200

// rememberFound keeps the documents offered by «+ Из найденного»: приложенное
// из них читается при отправке, даже если поиски чата успели переполнить
// свою выдачу (ревью #464).
func (d *chatDesk) rememberFound(docs []chat.Doc) {
	d.foundMu.Lock()
	defer d.foundMu.Unlock()
	if d.found == nil || len(d.found) > maxFound {
		d.found = map[string]chat.Doc{}
	}
	for _, doc := range docs {
		d.found[doc.Ref] = doc
	}
}

func (d *chatDesk) foundDoc(ref string) (chat.Doc, bool) {
	d.foundMu.Lock()
	defer d.foundMu.Unlock()
	doc, ok := d.found[ref]
	return doc, ok
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
func (d *chatDesk) Get(id string) (chatRecord, error) {
	rec, err := d.load(id)
	rec.CanCompress = canCompress(rec)
	rec.Context = d.contextOf(rec)
	return rec, err
}

// contextOf is the fill of rec's context: последний счет провайдера против
// размера окна модели из нынешних настроек; счета нет — nil.
func (d *chatDesk) contextOf(rec chatRecord) *contextNote {
	if rec.ContextUsed <= 0 {
		return nil
	}
	return &contextNote{Used: rec.ContextUsed, Window: d.config().ContextWindow}
}

// Delete removes a chat.
func (d *chatDesk) Delete(id string) error {
	p, err := d.path(id)
	if err != nil {
		return err
	}
	_ = d.DropDraft(id) // черновик не переживает свой чат
	d.dropChatProposals(id)
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
	rec.CanCompress = false // только в ответе страницы, в файл не пишется
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
	if c := rec.Compressed; c != nil {
		return chat.Compression{Text: c.Text, Folded: c.Folded}.Apply(out)
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
	case errors.Is(err, chat.ErrOutOfSteps):
		return fmt.Sprintf("модель сделала %d шагов поиска и не сформулировала ответ — уточните вопрос или назовите документ точнее", chat.MaxSteps)
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
	d.search = func(ctx context.Context, q string, limit int) ([]chat.Doc, error) {
		res := d.settings.SearchSources(ctx, q)
		snap := d.settings.snapshot().cfg.WithSources()
		entries := entryDocs(res)
		d.remember(entries)
		hits, err := files.Search(q, limit)
		if err != nil && !errors.Is(err, filesearch.ErrNotBuilt) {
			return nil, err
		}
		return interleave(entries, fileDocs(hits, snap.Sources, d.settings.home), limit), nil
	}
	d.read = func(ctx context.Context, ref string) (chat.Doc, error) {
		if p, ok := strings.CutPrefix(ref, "file:"); ok {
			text, err := files.Read(p)
			if err != nil {
				return chat.Doc{}, err
			}
			return chat.Doc{Ref: ref, Title: filepath.Base(p), Source: filepath.Dir(p), Text: text}, nil
		}
		d.seenMu.Lock()
		doc, ok := d.seen[ref]
		d.seenMu.Unlock()
		if !ok {
			return chat.Doc{}, fmt.Errorf("документа %q нет в выдаче поиска — найдите его сначала", ref)
		}
		// Ссылка — только пришедшая из поиска (находка Диска): ее текст читается
		// через подключение. Ссылку, которую модель придумала сама (подсказка
		// из чужого документа), окно не открывает (ревью #467, безопасность).
		if strings.HasPrefix(ref, "url:") {
			// Находка папки Диска — подключением своего источника (ревью #467).
			if conn := d.settings.driveConn(doc.SourceID); conn != "" && d.driveRead != nil {
				return d.driveRead(ctx, conn, ref)
			}
			if d.attach != nil {
				return d.attach(ctx, ref)
			}
		}
		return doc, nil
	}
}

// remember keeps the records of a search: модель читает только то, что видела.
func (d *chatDesk) remember(docs []chat.Doc) {
	d.seenMu.Lock()
	defer d.seenMu.Unlock()
	if d.seen == nil || len(d.seen) > maxSeenEntries {
		d.seen = map[string]chat.Doc{}
	}
	for _, doc := range docs {
		d.seen[doc.Ref] = doc
	}
}

// entryDocs turns the records of the merged search into chat documents.
func entryDocs(res sourcesSearch) []chat.Doc {
	names := map[string]string{}
	for _, r := range res.Sources {
		names[r.ID] = sourceLabel(r.Source)
	}
	var out []chat.Doc
	for _, h := range res.Hits {
		if doc, ok := entryDoc(h, names[h.Source]); ok {
			out = append(out, doc)
		}
	}
	return out
}

// fileDocs turns file hits into chat documents with their folder source.
func fileDocs(hits []filesearch.Hit, sources []desktopconfig.Source, home string) []chat.Doc {
	out := make([]chat.Doc, 0, len(hits))
	for _, h := range hits {
		out = append(out, chat.Doc{Ref: "file:" + h.Doc.Path(), Title: h.Doc.Name(), Source: filepath.Dir(h.Doc.Path()), Text: h.Snippet,
			SourceID: folderSourceOf(h.Doc.Path(), sources, home)})
	}
	return out
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
	ref := fmt.Sprintf("kb:%s:%d", h.Source, e.ID)
	if e.ID == 0 && e.URL != "" {
		ref = "url:" + e.URL // находка Диска: читается по ссылке через подключение
	}
	return chat.Doc{Ref: ref, Title: e.Title, Source: source, Text: strings.TrimSpace(b.String()), SourceID: h.Source}, true
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

// compressKeep — сколько последних сообщений остается живыми при сжатии.
const compressKeep = 6

// compressTimeout — сколько ждать сжатие целиком: длинная история идет
// несколькими вызовами подряд, а общий срок ответа (askTimeout) рассчитан на
// один ход.
const compressTimeout = 10 * time.Minute

// CompressChat folds the old part of chat id into a summary (#539). Сообщения
// в файле остаются целыми: конспект лежит рядом и подменяет начало только в
// запросе к модели. Повторное сжатие считает по исходным сообщениям, а не по
// прежнему конспекту.
//
// ponytail: повторное сжатие заново читает всю исходную историю. Потолок:
// чат на сотни сообщений дороже по токенам, чем сжатие поверх старого
// конспекта. Путь: сворачивать конспект и новую часть, сдвигая Folded.
func (d *chatDesk) CompressChat(id string) (chatRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, err := d.load(id)
	if err != nil {
		return chatRecord{}, err
	}
	c := d.config()
	key, err := d.modelKey(c)
	if err != nil {
		return chatRecord{}, err
	}
	// Сжато уже до этой точки (двойной щелчок, устаревшая страница): ответ тот же
	// и без платного вызова модели.
	if rec.Compressed != nil && rec.Compressed.Folded == len(rec.Messages)-compressKeep {
		rec.CanCompress = canCompress(rec)
		return rec, nil
	}
	raw := make([]chat.Message, 0, len(rec.Messages))
	for _, m := range rec.Messages {
		raw = append(raw, chat.Message{Role: chat.Role(m.Role), Text: m.Text})
	}
	ctx, cancel := context.WithTimeout(context.Background(), compressTimeout)
	defer cancel()
	model := d.metered(d.model(c, key), c, usage.Fixed(domain.CallCompress), id)
	sum, err := chat.NewService(nil, model).Compress(ctx, raw, compressKeep)
	switch {
	case errors.Is(err, chat.ErrNothingToCompress):
		return chatRecord{}, errors.New("чат короткий, сжимать нечего")
	case err != nil:
		return chatRecord{}, errors.New(chatError(err))
	}
	rec.Compressed = &chatCompressed{Text: sum.Text, Folded: sum.Folded}
	rec.ContextUsed = 0 // счет был до сжатия; новый придет с ближайшим ответом
	rec.Updated = d.now()
	if err := d.save(rec); err != nil {
		return chatRecord{}, err
	}
	rec.CanCompress = canCompress(rec)
	return rec, nil
}

// canCompress tells the page whether the chat has more than compressKeep
// messages: число «шесть» знает только окно.
func canCompress(rec chatRecord) bool {
	if rec.Compressed != nil && rec.Compressed.Folded >= len(rec.Messages)-compressKeep {
		return false // уже свернуто до хвоста: повторное сжатие вернуло бы то же
	}
	return len(rec.Messages) > compressKeep
}
