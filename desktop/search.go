package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// findFunc asks a kb server to search; таймаут задает контекст вызывающего.
type findFunc func(ctx context.Context, url, token, q string) ([]json.RawMessage, error)

// searchTimeout — сколько ждать сервер на одну букву запроса: медленный хост
// не держит выдачу каталога, он приходит в строку источников с «нет связи».
const searchTimeout = 3 * time.Second

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

// sourceFound is one knowledge-base source in the strip above the results:
// сколько он нашел или почему не ответил. «Показано» считает интерфейс — он
// и решает, сколько строк показать.
type sourceFound struct {
	desktopconfig.Source
	Found int    `json:"found"`
	Error string `json:"error,omitempty"`
}

type sourcesSearch struct {
	Hits    []sourceHit   `json:"hits"`
	Sources []sourceFound `json:"sources"`
	// Dupes — сколько записей склеено с источником выше: интерфейс по
	// «найдено» его не вычислит, выдача уже срезана.
	Dupes int `json:"dupes"`
}

// SearchSources asks every enabled knowledge-base source (catalogs and kb
// servers) in parallel and merges the answers by the list order.
// ponytail: папки в строку источников не входят — их выдача идет своим
// /api/files/search, счет «N из M» по папкам — следующий шаг этапа 5.
func (f *folderSettings) SearchSources(ctx context.Context, q string) sourcesSearch {
	f.mu.Lock()
	cfg := f.cfg
	f.mu.Unlock()
	var rows []sourceFound
	for _, s := range cfg.WithSources().Sources {
		if s.Enabled && (s.Kind == desktopconfig.KindCatalog || s.Kind == desktopconfig.KindServer) {
			rows = append(rows, sourceFound{Source: s})
		}
	}
	lists := make([]sourcesearch.Found[json.RawMessage], len(rows))
	var wg sync.WaitGroup
	for i := range rows {
		r := &rows[i]
		wg.Go(func() {
			var items []json.RawMessage
			var msg string
			if r.Kind == desktopconfig.KindCatalog {
				items, msg = searchCatalog(desktopconfig.ExpandHome(r.Path, f.home), q)
			} else {
				items, msg = f.searchServer(ctx, r.Source, q)
			}
			r.Found, r.Error = len(items), msg
			lists[i] = sourcesearch.Found[json.RawMessage]{Source: r.ID, Items: items[:min(len(items), searchLimit)]}
		})
	}
	wg.Wait()
	merged := sourcesearch.Merge(lists, entryKey)
	in := 0
	for _, l := range lists {
		in += len(l.Items)
	}
	out := sourcesSearch{Hits: make([]sourceHit, len(merged)), Sources: rows, Dupes: in - len(merged)}
	for i, h := range merged {
		out.Hits[i] = sourceHit{Source: h.Source, Entry: h.Item}
	}
	if out.Sources == nil {
		out.Sources = []sourceFound{}
	}
	return out
}

// searchCatalog searches one catalog file the way /api/search does.
func searchCatalog(path, q string) ([]json.RawMessage, string) {
	cat, err := catalogjson.FileLoader{Path: path}.Load()
	if err != nil {
		return nil, "каталог не прочитался: " + err.Error()
	}
	var out []json.RawMessage
	for _, e := range search.FilterWith(cat.Entries(), q, search.Matcher{}) {
		raw, err := httpapi.EntryJSON(e)
		if err != nil {
			return nil, "каталог не прочитался: " + err.Error()
		}
		out = append(out, raw)
	}
	return out, ""
}

// errSourceChanged — пока окно читало Связку, источник сменил адрес или токен.
const errSourceChanged = "настройки источника изменились: повторите поиск"

// searchServer asks one kb server with its stored token.
func (f *folderSettings) searchServer(ctx context.Context, s desktopconfig.Source, q string) ([]json.RawMessage, string) {
	tok, err := f.tokens.Get(s.ID)
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		// Связка не ответила — сервер без токена не спрашивается: ответ 401 на
		// каждую букву ничего бы не сказал, а беда названа своими словами.
		return nil, "Связка ключей не ответила: разблокируйте ее"
	}
	if !f.stillSame(s, tok) {
		return nil, errSourceChanged
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	raw, err := f.find(ctx, s.URL, tok, q)
	if err != nil {
		return nil, serverMessage(err)
	}
	return typedEntries(raw), ""
}

// stillSame reports whether server s still has this address and this token.
// Поиск идет по снимку конфига, а Связка читается позже: SetSources мог за это
// время сменить адрес и положить новый токен — и новый токен ушел бы на старый
// адрес. SetSources держит f.mu всю работу, поэтому сверка под f.mu видит либо
// старое целиком, либо новое целиком.
func (f *folderSettings) stillSame(s desktopconfig.Source, tok string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cur := range f.cfg.WithSources().Sources {
		if cur.ID == s.ID {
			now, err := f.tokens.Get(s.ID)
			if errors.Is(err, keychain.ErrNotFound) {
				now, err = "", nil
			}
			return cur.Kind == desktopconfig.KindServer && cur.URL == s.URL && err == nil && now == tok
		}
	}
	return false
}

// serverEntry is what the window shows of a server's entry. Запись чужого
// сервера разбирается строго: поле не того типа — запись отброшена (номер-
// строка с разметкой не доедет до страницы), наружу уходят только эти поля.
type serverEntry struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Category    string `json:"category,omitempty"`
	Verdict     string `json:"verdict,omitempty"`
	Author      string `json:"author,omitempty"`
	DateAdded   string `json:"date_added,omitempty"`
	Description string `json:"description,omitempty"`
}

// typedEntries keeps the server entries that decode as serverEntry and
// re-encodes them; an address that is not http(s) is dropped — javascript:
// не откроется кнопкой «Открыть источник».
func typedEntries(raw []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(raw))
	for _, r := range raw {
		var e serverEntry
		if json.Unmarshal(r, &e) != nil || e.Title == "" {
			continue
		}
		if u, err := url.Parse(e.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			e.URL = ""
		}
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	return out
}

// entryKey reads the identity fields of an entry for the merge.
func entryKey(raw json.RawMessage) string {
	var e struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return "" // не опознать — не склеивать
	}
	return sourcesearch.EntryKey(e.ID, e.Title, e.URL)
}

// searchRoute serves /api/sources/search and passes the rest to the engine's
// API. Маршрут HTTP, а не привязка Wails: интерфейс тот же в браузере (-browser).
func (f *folderSettings) searchRoute(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sources/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q is required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.SearchSources(r.Context(), q))
	})
	mux.Handle("/", next)
	return mux
}

// findServer asks a real kb server.
func findServer(ctx context.Context, url, token, q string) ([]json.RawMessage, error) {
	return kbserver.Client{URL: url, Token: token}.Search(ctx, q)
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
}

type cachedToken struct {
	val     string
	missing bool
}

func newCachedTokens(inner tokenStore) *cachedTokens {
	return &cachedTokens{inner: inner, m: map[string]cachedToken{}}
}

func (c *cachedTokens) Get(account string) (string, error) {
	c.mu.Lock()
	v, ok := c.m[account]
	gen := c.gen
	c.mu.Unlock()
	if ok {
		if v.missing {
			return "", keychain.ErrNotFound
		}
		return v.val, nil
	}
	val, err := c.inner.Get(account)
	switch {
	case err == nil:
		c.putIf(gen, account, cachedToken{val: val})
	case errors.Is(err, keychain.ErrNotFound):
		c.putIf(gen, account, cachedToken{missing: true})
	}
	return val, err
}

func (c *cachedTokens) Set(account, secret string) error {
	if err := c.inner.Set(account, secret); err != nil {
		c.forget(account) // что лежит в Связке после отказа, неизвестно
		return err
	}
	c.mu.Lock()
	c.gen++
	c.m[account] = cachedToken{val: secret}
	c.mu.Unlock()
	return nil
}

func (c *cachedTokens) Delete(account string) error {
	err := c.inner.Delete(account)
	c.forget(account)
	return err
}

// putIf stores v unless a write or delete happened since gen was read.
func (c *cachedTokens) putIf(gen uint64, account string, v cachedToken) {
	c.mu.Lock()
	if c.gen == gen {
		c.m[account] = v
	}
	c.mu.Unlock()
}

func (c *cachedTokens) forget(account string) {
	c.mu.Lock()
	c.gen++
	delete(c.m, account)
	c.mu.Unlock()
}
