package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
			lists[i] = sourcesearch.Found[json.RawMessage]{Source: r.ID, Items: items}
		})
	}
	wg.Wait()
	merged := sourcesearch.Merge(lists, entryKey)
	out := sourcesSearch{Hits: make([]sourceHit, len(merged)), Sources: rows}
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

// searchServer asks one kb server with its stored token.
func (f *folderSettings) searchServer(ctx context.Context, s desktopconfig.Source, q string) ([]json.RawMessage, string) {
	tok, err := f.tokens.Get(s.ID)
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		// Связка не ответила — сервер без токена не спрашивается: ответ 401 на
		// каждую букву ничего бы не сказал, а беда названа своими словами.
		return nil, "Связка ключей не ответила: разблокируйте ее"
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	items, err := f.find(ctx, s.URL, tok, q)
	if err != nil {
		return nil, serverMessage(err)
	}
	return items, ""
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
		c.put(account, cachedToken{val: val})
	case errors.Is(err, keychain.ErrNotFound):
		c.put(account, cachedToken{missing: true})
	}
	return val, err
}

func (c *cachedTokens) Set(account, secret string) error {
	if err := c.inner.Set(account, secret); err != nil {
		c.forget(account) // что лежит в Связке после отказа, неизвестно
		return err
	}
	c.put(account, cachedToken{val: secret})
	return nil
}

func (c *cachedTokens) Delete(account string) error {
	err := c.inner.Delete(account)
	c.forget(account)
	return err
}

func (c *cachedTokens) put(account string, v cachedToken) {
	c.mu.Lock()
	c.m[account] = v
	c.mu.Unlock()
}

func (c *cachedTokens) forget(account string) {
	c.mu.Lock()
	delete(c.m, account)
	c.mu.Unlock()
}
