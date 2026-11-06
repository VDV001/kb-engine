package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/sourcelist"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// findFunc asks a kb server to search; таймаут задает контекст вызывающего.
type findFunc func(ctx context.Context, url, token, q string) (kbserver.Result, error)

// searchTimeout — сколько ждать на одну букву запроса чтение токена и, отдельно,
// ответ сервера (до двух сроков подряд): медленный хост или запертая Связка не
// держат выдачу каталога.
// Переменная, а не константа, — тесты ждут миллисекунды, а не секунды.
var searchTimeout = 3 * time.Second

// searchLimit — сколько записей одного источника идет в выдачу; «найдено»
// считается полностью. Широкий запрос не тянет в окно весь каталог, а
// источник ниже по приоритету не вытесняется верхним целиком: фильтр по нему
// покажет его записи.
const searchLimit = 40

// sourceHit is one entry of the merged results with the source it came from.
type sourceHit struct {
	Source string          `json:"source"`
	Entry  json.RawMessage `json:"entry"`
}

// sourceFound is one source in the strip above the results:
// сколько он нашел или почему не ответил. «Показано» считает интерфейс — он
// и решает, сколько строк показать.
type sourceFound struct {
	desktopconfig.Source
	Found int    `json:"found"`
	Error string `json:"error,omitempty"`
	// Root — раскрытый путь папки: по нему окно считает, сколько ее файлов
	// показано в выдаче.
	Root string `json:"root,omitempty"`
}

type sourcesSearch struct {
	Hits    []sourceHit   `json:"hits"`
	Sources []sourceFound `json:"sources"`
	// Dupes — сколько записей склеено с источником выше: интерфейс по
	// «найдено» его не вычислит, выдача уже срезана.
	Dupes int `json:"dupes"`
}

// candidate is one found entry before the merge: ключ для склейки и
// кодирование, отложенное до среза, — кодируются только показанные записи.
type candidate struct {
	key    string
	encode func() (json.RawMessage, error)
}

// SearchSources asks every enabled source in parallel — catalogs and kb servers
// for entries, folders for counts — and merges the entries by the list order. Склейка видит
// все найденное, срез до searchLimit — после нее: иначе дубль за пределом
// среза у источника выше показался бы у нижнего.
func (f *folderSettings) SearchSources(ctx context.Context, q string) sourcesSearch {
	snap := f.snapshot()
	rows := sourceRows(snap.cfg)
	lists := make([]sourcesearch.Found[candidate], len(rows))
	var wg sync.WaitGroup
	for i := range rows {
		r := &rows[i]
		wg.Go(func() {
			var items []candidate
			var msg string
			switch r.Kind {
			case desktopconfig.KindCatalog:
				items, msg = searchCatalog(desktopconfig.ExpandHome(r.Path, f.home), q)
			case desktopconfig.KindServer:
				items, msg = f.searchServer(ctx, r.Source, snap, q)
			default:
				return // папки считает countFolders: их выдача — /api/files/search
			}
			r.Found, r.Error = len(items), msg
			lists[i] = sourcesearch.Found[candidate]{Source: r.ID, Items: items}
		})
	}
	f.countFolders(rows, q)
	wg.Wait()
	merged := sourcesearch.Merge(lists, func(c candidate) string { return c.key })
	total := 0
	for _, l := range lists {
		total += len(l.Items)
	}
	out := sourcesSearch{Hits: []sourceHit{}, Sources: rows, Dupes: total - len(merged)}
	shown := map[string]int{}
	for _, h := range merged {
		if shown[h.Source] == searchLimit {
			continue
		}
		// Кодирование простой структуры (строки, числа) не падает; запись, что
		// известна только ключом, в выдачу не идет намеренно.
		raw, err := h.Item.encode()
		if err != nil {
			continue
		}
		shown[h.Source]++
		out.Hits = append(out.Hits, sourceHit{Source: h.Source, Entry: raw})
	}
	if out.Sources == nil {
		out.Sources = []sourceFound{}
	}
	return out
}

// countFolders fills the folder rows: сколько файлов каждой открытой папки
// нашел запрос (без среза выдачи); папка, что сейчас не открыта в индексе
// (сетевой диск без VPN), названа, а не пропущена. Файлы выдачи окно берет из
// /api/files/search — здесь только счет.
func (f *folderSettings) countFolders(rows []sourceFound, q string) {
	roots := f.markOpen(rows)
	if len(roots) == 0 || f.countMatches == nil {
		return
	}
	if f.indexing != nil && f.indexing() {
		markRebuilding(rows)
		return
	}
	counts, err := f.countMatches(q, roots)
	for i := range rows {
		if rows[i].Kind != desktopconfig.KindFolder || rows[i].Error != "" {
			continue
		}
		switch {
		case errors.Is(err, filesearch.ErrNotBuilt):
			rows[i].Error = "индекс файлов собирается"
		case err != nil:
			rows[i].Error = "индекс файлов не собрался"
		default:
			rows[i].Found = counts[rows[i].Root]
		}
	}
}

// sourceRows lists the enabled sources of a known kind, in list order — те же,
// что видит MCP (sourcelist.Enabled).
func sourceRows(cfg desktopconfig.Config) []sourceFound {
	var rows []sourceFound
	for _, s := range sourcelist.Enabled(cfg) {
		rows = append(rows, sourceFound{Source: s})
	}
	return rows
}

// markRebuilding names the open folders while the index is rebuilding: их счет
// старый, и только что добавленная папка в нем еще пуста.
func markRebuilding(rows []sourceFound) {
	for i := range rows {
		if rows[i].Kind == desktopconfig.KindFolder && rows[i].Error == "" {
			rows[i].Error = "индекс файлов пересобирается"
		}
	}
}

// isDir reports whether path is a directory right now.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// markOpen sets the root of every folder row, names the folders that are not
// open in the index, and returns the open roots.
func (f *folderSettings) markOpen(rows []sourceFound) []string {
	open := map[string]bool{}
	if p := f.open.Load(); p != nil {
		for _, r := range *p {
			open[r] = true
		}
	}
	var roots []string
	for i := range rows {
		if rows[i].Kind != desktopconfig.KindFolder {
			continue
		}
		rows[i].Root = desktopconfig.ExpandHome(rows[i].Path, f.home)
		// Открыта в индексе и есть на диске сейчас: пропавшая разом единственная
		// сетевая папка остается в open (reconcile пустой набор не применяет).
		if open[rows[i].Root] && isDir(rows[i].Root) {
			roots = append(roots, rows[i].Root)
		} else {
			rows[i].Error = "нет связи: папка недоступна (диск, VPN)"
		}
	}
	return roots
}

// searchCatalog searches one catalog file the way /api/search does.
func searchCatalog(path, q string) ([]candidate, string) {
	found, err := sourcelist.SearchCatalog(path, q, search.Matcher{})
	if err != nil {
		return nil, "каталог не прочитался: " + err.Error()
	}
	out := make([]candidate, len(found))
	for i, e := range found {
		out[i] = candidate{
			key: sourcesearch.EntryKey(e.ID(), e.Title(), e.URL()),
			// Объяснение — только для показанных: encode зовется после среза.
			encode: func() (json.RawMessage, error) { return httpapi.EntryJSON(e, search.Explain(e, q, search.Matcher{})) },
		}
	}
	return out, ""
}

// errSourceChanged — пока окно читало Связку, источники сохранили заново или
// сохраняют сейчас.
const errSourceChanged = "настройки источников изменились: повторите поиск"

// cfgSnapshot is the settings the search reads; saving — SetSources пишет
// токены, и Связка может не совпадать с адресами снимка.
type cfgSnapshot struct {
	cfg    desktopconfig.Config
	saving bool
}

// publishLocked stores the current settings as the search snapshot; f.mu held.
func (f *folderSettings) publishLocked(saving bool) {
	f.snap.Store(&cfgSnapshot{cfg: f.cfg, saving: saving})
}

// snapshot returns the published settings, publishing them on first use.
// В окне main публикует их сразу, так что f.mu здесь не берется.
func (f *folderSettings) snapshot() *cfgSnapshot {
	if p := f.snap.Load(); p != nil {
		return p
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snap.Load() == nil {
		f.publishLocked(false)
	}
	return f.snap.Load()
}

// publish stores the current settings as the search snapshot.
func (f *folderSettings) publish() {
	f.mu.Lock()
	f.publishLocked(false)
	f.mu.Unlock()
}

// searchServer asks one kb server with its stored token. snap — снимок
// настроек, по которому идет поиск.
func (f *folderSettings) searchServer(ctx context.Context, s desktopconfig.Source, snap *cfgSnapshot, q string) ([]candidate, string) {
	if snap.saving {
		return nil, errSourceChanged
	}
	// У Связки и у сети — по своему сроку, как в ServerStates: человек,
	// разблокировавший Связку под конец срока, не получает «нет связи».
	tokCtx, cancelTok := context.WithTimeout(ctx, searchTimeout)
	tok, err := sourcelist.TokenWithin(tokCtx, f.tokens.Get, s.ID)
	cancelTok()
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		// Связка не ответила — сервер без токена не спрашивается: ответ 401 на
		// каждую букву ничего бы не сказал, а беда названа своими словами.
		return nil, keychain.Locked
	}
	// Поиск идет по снимку конфига, а Связка читается позже: SetSources мог за
	// это время сменить адрес и положить новый токен — и новый токен ушел бы на
	// старый адрес. Перед записью токенов SetSources публикует новый снимок,
	// поэтому тот же указатель значит: с момента снимка токены не менялись.
	if f.snap.Load() != snap {
		return nil, errSourceChanged
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	res, err := f.find(ctx, s.URL, tok, q)
	if err != nil {
		return nil, serverMessage(err)
	}
	// Записи сверх присланных известны только ключами: они склеиваются с
	// нижними источниками и входят в «найдено», но не показываются.
	items := typedEntries(res.Entries)
	for _, k := range res.Keys {
		items = append(items, candidate{key: k, encode: keyOnly})
	}
	return items, ""
}

// errKeyOnly — у записи есть только ключ: сервер прислал ее для склейки.
var errKeyOnly = errors.New("запись известна только ключом")

func keyOnly() (json.RawMessage, error) { return nil, errKeyOnly }

// typedEntries is the server's entries decoded strictly (sourcelist), as
// candidates: кодируется запись только при показе.
func typedEntries(raw []json.RawMessage) []candidate {
	dec := sourcelist.DecodeEntries(raw)
	out := make([]candidate, len(dec))
	for i, d := range dec {
		out[i] = candidate{key: d.Key, encode: func() (json.RawMessage, error) { return json.Marshal(d.Entry) }}
	}
	return out
}

// searchRoute serves /api/sources/search and passes the rest to the engine's
// API. Маршрут HTTP, а не привязка Wails: интерфейс тот же в браузере (-browser).
func (f *folderSettings) searchRoute(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	// Чужой Host отсекает browserHandler на весь -browser; окно Wails из сети
	// недостижимо. Второй замок здесь был бы второй копией одного правила.
	mux.Handle("GET /api/sources/search", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q is required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.SearchSources(r.Context(), q))
	}))
	mux.Handle("/", next)
	return mux
}

// findServer asks a real kb server for twice the entries the window shows:
// первые записи сервера часто дублируют источник выше и склеиваются, и без
// запаса от сервера осталась бы горстка строк при сотнях найденных.
// ponytail: запас — эвристика. Потолок: если перекрытие с источниками выше
// глубже 2·searchLimit записей, сервер покажет меньше searchLimit при большем
// числе своих. Путь: дозапрос, когда показано меньше предела, а ключей
// остальных много, — или ключи источников выше в запросе.
func findServer(ctx context.Context, url, token, q string) (kbserver.Result, error) {
	return kbserver.Client{URL: url, Token: token}.Search(ctx, q, 2*searchLimit)
}

// cachedTokens keeps what the Keychain answered in memory: поиск идет на каждую
// букву, и без кэша каждая запускала бы security. Запоминаются токен и «записи
// нет»; сбой чтения (Связка заперта) — нет, следующий запрос спросит снова.
// Запись и удаление идут через кэш, поэтому он не расходится с тем, что окно
// само положило. ponytail: правку токена в «Связке ключей» руками окно увидит
// только после перезапуска; путь — сбрасывать кэш при открытии настроек.
type cachedTokens struct {
	inner tokenStore
	mu    sync.Mutex
	m     map[string]cachedToken
	// gen растет при каждой записи и удалении: чтение Связки, начатое до них,
	// свой результат в кэш не кладет — иначе удаленный токен вернулся бы.
	gen uint64
	// flights — чтения Связки, что идут сейчас: пока Связка думает, буквы
	// запроса ждут одно чтение учетки, а не запускают по security на каждую.
	flights map[string]*flight
}

type flight struct {
	done chan struct{}
	val  string
	err  error
}

type cachedToken struct {
	val     string
	missing bool
}

func newCachedTokens(inner tokenStore) *cachedTokens {
	return &cachedTokens{inner: inner, m: map[string]cachedToken{}, flights: map[string]*flight{}}
}

func (c *cachedTokens) Get(account string) (string, error) {
	c.mu.Lock()
	if v, ok := c.m[account]; ok {
		c.mu.Unlock()
		if v.missing {
			return "", keychain.ErrNotFound
		}
		return v.val, nil
	}
	if fl, ok := c.flights[account]; ok {
		c.mu.Unlock()
		<-fl.done
		return fl.val, fl.err
	}
	fl := &flight{done: make(chan struct{})}
	c.flights[account] = fl
	gen := c.gen
	c.mu.Unlock()

	fl.val, fl.err = c.inner.Get(account)
	c.mu.Lock()
	if c.flights[account] == fl {
		delete(c.flights, account)
	}
	if c.gen == gen {
		switch {
		case fl.err == nil:
			c.m[account] = cachedToken{val: fl.val}
		case errors.Is(fl.err, keychain.ErrNotFound):
			c.m[account] = cachedToken{missing: true}
		}
	}
	c.mu.Unlock()
	close(fl.done)
	return fl.val, fl.err
}

func (c *cachedTokens) Set(account, secret string) error {
	if err := c.inner.Set(account, secret); err != nil {
		c.forget(account) // что лежит в Связке после отказа, неизвестно
		return err
	}
	c.mu.Lock()
	c.gen++
	c.m[account] = cachedToken{val: secret}
	delete(c.flights, account) // следующий Get не встанет в ожидание старого чтения
	c.mu.Unlock()
	return nil
}

func (c *cachedTokens) Delete(account string) error {
	err := c.inner.Delete(account)
	c.forget(account)
	return err
}

func (c *cachedTokens) forget(account string) {
	c.mu.Lock()
	c.gen++
	delete(c.m, account)
	delete(c.flights, account)
	c.mu.Unlock()
}
