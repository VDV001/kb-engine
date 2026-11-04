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
	"github.com/daniil/kb-engine/internal/domain"
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

// candidate is one found entry before the merge: ключ для склейки и
// кодирование, отложенное до среза, — кодируются только показанные записи.
type candidate struct {
	key    string
	encode func() (json.RawMessage, error)
}

// SearchSources asks every enabled knowledge-base source (catalogs and kb
// servers) in parallel and merges the answers by the list order. Склейка видит
// все найденное, срез до searchLimit — после нее: иначе дубль за пределом
// среза у источника выше показался бы у нижнего.
// ponytail: папки в строку источников не входят — их выдача идет своим
// /api/files/search, счет «N из M» по папкам — следующий шаг этапа 5.
func (f *folderSettings) SearchSources(ctx context.Context, q string) sourcesSearch {
	snap := f.snapshot()
	var rows []sourceFound
	for _, s := range snap.cfg.WithSources().Sources {
		if s.Enabled && (s.Kind == desktopconfig.KindCatalog || s.Kind == desktopconfig.KindServer) {
			rows = append(rows, sourceFound{Source: s})
		}
	}
	lists := make([]sourcesearch.Found[candidate], len(rows))
	var wg sync.WaitGroup
	for i := range rows {
		r := &rows[i]
		wg.Go(func() {
			var items []candidate
			var msg string
			if r.Kind == desktopconfig.KindCatalog {
				items, msg = searchCatalog(desktopconfig.ExpandHome(r.Path, f.home), q)
			} else {
				items, msg = f.searchServer(ctx, r.Source, snap, q)
			}
			r.Found, r.Error = len(items), msg
			lists[i] = sourcesearch.Found[candidate]{Source: r.ID, Items: items}
		})
	}
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

// searchCatalog searches one catalog file the way /api/search does.
func searchCatalog(path, q string) ([]candidate, string) {
	cat, err := catalogjson.FileLoader{Path: path}.Load()
	if err != nil {
		return nil, "каталог не прочитался: " + err.Error()
	}
	found := search.FilterWith(cat.Entries(), q, search.Matcher{})
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
	tok, err := f.tokenWithin(tokCtx, s.ID)
	cancelTok()
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		// Связка не ответила — сервер без токена не спрашивается: ответ 401 на
		// каждую букву ничего бы не сказал, а беда названа своими словами.
		return nil, "Связка ключей не ответила: разблокируйте ее"
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

// tokenWithin reads a token, giving up when ctx ends: Связка, ждущая
// разблокировки, не держит выдачу. Брошенное чтение доживает само (security
// ограничен 10 с), его результат кладет в кэш cachedTokens.
func (f *folderSettings) tokenWithin(ctx context.Context, id string) (string, error) {
	type res struct {
		tok string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		t, err := f.tokens.Get(id)
		ch <- res{t, err}
	}()
	select {
	case r := <-ch:
		return r.tok, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// serverEntry is what the window shows of a server's entry. Запись чужого
// сервера разбирается строго: поле не того типа — запись отброшена (номер-
// строка с разметкой не доедет до страницы), наружу уходят только эти поля.
type serverEntry struct {
	ID          int           `json:"id"`
	Title       string        `json:"title"`
	URL         string        `json:"url,omitempty"`
	Category    string        `json:"category,omitempty"`
	Verdict     string        `json:"verdict,omitempty"`
	Author      string        `json:"author,omitempty"`
	DateAdded   string        `json:"date_added,omitempty"`
	Description string        `json:"description,omitempty"`
	Match       []serverMatch `json:"match,omitempty"`
}

// Пределы признака находки чужого сервера: из слов признака окно строит
// подсветку на каждую строку выдачи, и сломанный сервер не должен подвесить ее
// тысячей признаков или словом-простыней. Наш сервер дает по признаку на слово
// запроса — единицы.
const (
	maxMatches  = 16
	maxMatchLen = 200
)

// withoutMatch returns the entry with its match field removed: запись
// разбирается строго, признак — отдельно.
func withoutMatch(r json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(r, &m) != nil {
		return r
	}
	delete(m, "match")
	b, err := json.Marshal(m)
	if err != nil {
		return r
	}
	return b
}

// serverMatch is one «где и как совпало» of a server's entry; поле и способ
// — из тех, что называет search.Explain, иначе признак отброшен.
type serverMatch struct {
	Word  string `json:"word"`
	Field string `json:"field"`
	Hit   string `json:"hit"`
	How   string `json:"how"`
}

// knownMatches keeps the matches with a field and way search can name: список
// живет в пакете search, а не здесь третьей копией.
func knownMatches(in []serverMatch) []serverMatch {
	var out []serverMatch
	for _, m := range in {
		if len(out) == maxMatches {
			break
		}
		if search.ValidField(m.Field) && search.ValidHow(m.How) && len(m.Word) <= maxMatchLen && len(m.Hit) <= maxMatchLen {
			out = append(out, m)
		}
	}
	return out
}

// typedEntries keeps the server entries that decode as serverEntry; адрес, не
// прошедший правило домена (http(s) с хостом), убирается — javascript: не
// откроется кнопкой «Открыть источник». Кодируется запись только при показе.
func typedEntries(raw []json.RawMessage) []candidate {
	out := make([]candidate, 0, len(raw))
	for _, r := range raw {
		// Признак — необязательное поле и разбирается отдельно: не того вида,
		// он отбрасывается сам, а запись с ключом склейки остается.
		var e serverEntry
		var rawMatch struct {
			Match json.RawMessage `json:"match"`
		}
		if json.Unmarshal(r, &rawMatch) != nil {
			continue
		}
		if len(rawMatch.Match) > 0 {
			r = withoutMatch(r)
		}
		if json.Unmarshal(r, &e) != nil || e.Title == "" {
			continue
		}
		var ms []serverMatch
		_ = json.Unmarshal(rawMatch.Match, &ms) // не тот вид — признака нет
		// Ключ — по исходному адресу, как ключи остальных записей, что сервер
		// считает сам (sourcesearch.Split): склейка не зависит от того, попала
		// запись в присланные или нет. Показ — по проверенному адресу.
		key := sourcesearch.EntryKey(e.ID, e.Title, e.URL)
		e.Match = knownMatches(ms)
		if u, err := domain.NewExternalURL(e.URL); err != nil {
			e.URL = ""
		} else {
			e.URL = u.String() // обрезанный: и для кнопки, и для ключа склейки
		}
		out = append(out, candidate{
			key:    key,
			encode: func() (json.RawMessage, error) { return json.Marshal(e) },
		})
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
