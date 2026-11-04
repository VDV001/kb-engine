package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// searchCall — что окно спросило у сервера при поиске.
type searchCall struct{ url, token, q string }

// newSearchSettings — окно с сервером выше каталога: сервер отдает две записи,
// одна из них есть и в каталоге.
func newSearchSettings(t *testing.T) (*folderSettings, *fakeTokens, *[]searchCall) {
	t.Helper()
	s, _, home := newSourcesSettings(t)
	cat := `{"entries":[
		{"id":1,"title":"Агент общий","url":"https://example.com/a","category":"ai","status":"keep","lifecycle":"active"},
		{"id":2,"title":"Агент каталога","url":"https://example.com/b","category":"ai","status":"keep","lifecycle":"active"},
		{"id":3,"title":"Другое","url":"https://example.com/c","category":"ai","status":"keep","lifecycle":"active"}]}`
	if err := os.WriteFile(filepath.Join(home, "catalog.json"), []byte(cat), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cfg.Sources = []desktopconfig.Source{
		src("server-a", desktopconfig.KindServer, "https://kb.example.ru", true),
		src("catalog-1", desktopconfig.KindCatalog, filepath.Join(home, "catalog.json"), true),
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
	}
	tok := &fakeTokens{m: map[string]string{"server-a": srvToken}}
	s.tokens = tok
	var calls []searchCall
	var mu sync.Mutex // источники опрашиваются параллельно
	s.find = func(_ context.Context, u, token, q string) (kbserver.Result, error) {
		mu.Lock()
		calls = append(calls, searchCall{u, token, q})
		mu.Unlock()
		switch {
		case strings.Contains(u, "down"):
			return kbserver.Result{}, kbserver.ErrUnreachable
		case token != srvToken:
			return kbserver.Result{}, kbserver.ErrUnauthorized
		}
		return result(
			json.RawMessage(`{"id":1,"title":"Агент общий","url":"https://example.com/a"}`),
			json.RawMessage(`{"id":9,"title":"Агент сервера"}`),
		), nil
	}
	return s, tok, &calls
}

// result is what a kb server of 0.30.0 answers: everything found, no keys.
func result(entries ...json.RawMessage) kbserver.Result {
	return kbserver.Result{Entries: entries, Total: len(entries)}
}

// hitIDs renders hits as "source:id" for a compact comparison.
func hitIDs(t *testing.T, r sourcesSearch) []string {
	t.Helper()
	out := make([]string, len(r.Hits))
	for i, h := range r.Hits {
		var e struct{ ID int }
		if err := json.Unmarshal(h.Entry, &e); err != nil {
			t.Fatalf("запись %s: %v", h.Entry, err)
		}
		out[i] = h.Source + ":" + strconv.Itoa(e.ID)
	}
	return out
}

func found(r sourcesSearch) map[string]sourceFound {
	m := map[string]sourceFound{}
	for _, s := range r.Sources {
		m[s.ID] = s
	}
	return m
}

// Выдача — один список: источник выше идет первым, запись, которая есть и на
// сервере, и в каталоге, показывается раз — у сервера. Счет «найдено» у
// каждого источника свой, по тому, что он нашел сам.
func TestSearchSources_MergesByPriority(t *testing.T) {
	s, _, calls := newSearchSettings(t)
	r := s.SearchSources(context.Background(), "агент")
	if got, want := hitIDs(t, r), []string{"server-a:1", "server-a:9", "catalog-1:2"}; !slices.Equal(got, want) {
		t.Fatalf("hits = %q, want %q", got, want)
	}
	if ids := []string{r.Sources[0].ID, r.Sources[1].ID}; len(r.Sources) != 2 || !slices.Equal(ids, []string{"server-a", "catalog-1"}) {
		t.Fatalf("sources = %+v: нужны только базы знаний в порядке списка", r.Sources)
	}
	if f := found(r); f["server-a"].Found != 2 || f["catalog-1"].Found != 2 || f["server-a"].Error != "" || f["catalog-1"].Error != "" {
		t.Fatalf("sources = %+v", r.Sources)
	}
	if len(*calls) != 1 || (*calls)[0] != (searchCall{"https://kb.example.ru", srvToken, "агент"}) {
		t.Fatalf("calls = %+v", *calls)
	}
}

// Источник с бедой назван своими словами, а не пропущен молча, и остальные
// источники отвечают как обычно.
func TestSearchSources_States(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s *folderSettings, tok *fakeTokens, home string)
		source  string
		wantErr string
		asked   int // сколько раз окно спросило сервер
	}{
		{"нет связи", func(s *folderSettings, _ *fakeTokens, _ string) {
			s.cfg.Sources[0].URL = "https://down.example.ru"
		}, "server-a", "нет связи", 1},
		{"нет токена", func(_ *folderSettings, tok *fakeTokens, _ string) {
			delete(tok.m, "server-a")
		}, "server-a", "401", 1},
		// Запертая Связка — не «токена нет»: сервер без токена не спрашивается,
		// иначе каждая буква запроса уходила бы ему впустую.
		{"Связка не ответила", func(_ *folderSettings, tok *fakeTokens, _ string) {
			tok.getErr = errors.New("user interaction is not allowed")
		}, "server-a", "Связка ключей", 0},
		{"каталог не прочитался", func(_ *folderSettings, _ *fakeTokens, home string) {
			_ = os.Remove(filepath.Join(home, "catalog.json"))
		}, "catalog-1", "каталог", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, tok, calls := newSearchSettings(t)
			tt.setup(s, tok, s.home)
			r := s.SearchSources(context.Background(), "агент")
			f := found(r)
			if bad := f[tt.source]; !strings.Contains(bad.Error, tt.wantErr) || bad.Found != 0 {
				t.Fatalf("%s = %+v, want ошибку со словами %q", tt.source, bad, tt.wantErr)
			}
			for id, ok := range f {
				if id != tt.source && ok.Error != "" {
					t.Fatalf("беда одного источника задела %s: %+v", id, ok)
				}
			}
			if len(*calls) != tt.asked {
				t.Fatalf("сервер спрошен %d раз, want %d", len(*calls), tt.asked)
			}
		})
	}
}

// Выключенный источник не спрашивается и в строке источников не стоит.
func TestSearchSources_DisabledIsSilent(t *testing.T) {
	s, _, calls := newSearchSettings(t)
	s.cfg.Sources[0].Enabled = false
	r := s.SearchSources(context.Background(), "агент")
	if len(*calls) != 0 || len(r.Sources) != 1 || r.Sources[0].ID != "catalog-1" {
		t.Fatalf("calls = %d, sources = %+v", len(*calls), r.Sources)
	}
	if got := hitIDs(t, r); !slices.Equal(got, []string{"catalog-1:1", "catalog-1:2"}) {
		t.Fatalf("hits = %q", got)
	}
}

// Маршрут окна: /api/sources/search отвечает общей выдачей, остальное идет в
// API движка как раньше; пустой запрос — 400, как у поиска по файлам.
func TestSearchRoute(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("engine " + r.URL.Path)) })
	h := s.searchRoute(inner)
	local := func(r *http.Request) *http.Request { r.Host = "127.0.0.1:18090"; return r }

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, local(httptest.NewRequest(http.MethodGet, "/api/sources/search?q="+url.QueryEscape("агент"), nil)))
	var got sourcesSearch
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("code %d, err %v, body %s", rec.Code, err, rec.Body)
	}
	if len(got.Hits) != 3 || len(got.Sources) != 2 {
		t.Fatalf("body = %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, local(httptest.NewRequest(http.MethodGet, "/api/sources/search?q=", nil)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("пустой запрос: code %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil))
	if rec.Body.String() != "engine /api/search" {
		t.Fatalf("остальные пути: %q", rec.Body)
	}
}

// Токен читается из Связки один раз: поиск идет на каждую букву, и без кэша
// каждая запускала бы security, а при запертой Связке — ее диалог. Сбой
// чтения не запоминается: разблокировали Связку — следующий поиск ее прочтет.
func TestCachedTokens(t *testing.T) {
	inner := &countingTokens{fakeTokens: fakeTokens{m: map[string]string{"a": "t1"}}}
	c := newCachedTokens(inner)
	for range 3 {
		if v, err := c.Get("a"); v != "t1" || err != nil {
			t.Fatalf("Get = %q, %v", v, err)
		}
	}
	if _, err := c.Get("none"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("нет записи: %v", err)
	}
	_, _ = c.Get("none")
	if inner.gets != 2 {
		t.Fatalf("Связка прочитана %d раз, want 2 (по разу на учетку)", inner.gets)
	}
	if err := c.Set("a", "t2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("a"); v != "t2" {
		t.Fatalf("после Set: %q", v)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("a"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("после Delete: %v", err)
	}

	locked := &countingTokens{fakeTokens: fakeTokens{m: map[string]string{"a": "t1"}, getErr: errors.New("locked")}}
	c = newCachedTokens(locked)
	_, _ = c.Get("a")
	locked.getErr = nil
	if v, err := c.Get("a"); v != "t1" || err != nil {
		t.Fatalf("после разблокировки: %q, %v", v, err)
	}
}

// countingTokens counts reads of the underlying store.
type countingTokens struct {
	fakeTokens
	gets int
}

func (c *countingTokens) Get(acct string) (string, error) {
	c.gets++
	return c.fakeTokens.Get(acct)
}

// У каждого источника в выдачу идет не больше searchLimit записей, а счет
// «найдено» — полный: широкий запрос не тянет в окно весь удаленный каталог,
// а источник ниже по приоритету не вытесняется верхним целиком.
// Склеенные дубли считает Go: «найдено» после среза их уже не покажет.
func TestSearchSources_LimitPerSource(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.find = func(context.Context, string, string, string) (kbserver.Result, error) {
		out := []json.RawMessage{json.RawMessage(`{"id":1,"title":"Агент общий","url":"https://example.com/a"}`)}
		for i := range searchLimit + 20 {
			out = append(out, json.RawMessage(`{"id":`+strconv.Itoa(100+i)+`,"title":"с`+strconv.Itoa(i)+`"}`))
		}
		return result(out...), nil
	}
	r := s.SearchSources(context.Background(), "агент")
	per := map[string]int{}
	for _, h := range r.Hits {
		per[h.Source]++
	}
	if f := found(r); f["server-a"].Found != searchLimit+21 || per["server-a"] != searchLimit || per["catalog-1"] != 1 {
		t.Fatalf("found %+v, в выдаче %v", r.Sources, per)
	}
	if r.Dupes != 1 {
		t.Fatalf("dupes = %d, want 1", r.Dupes)
	}
}

// Запись чужого сервера проходит строгий разбор: поле не того типа — запись
// отброшена (номер-строка с разметкой не доедет до innerHTML), адрес не
// http(s) — убран (javascript: не откроется кнопкой «Открыть источник»).
// Наружу уходят только поля, которые показывает окно.
func TestSearchSources_ServerEntriesAreTyped(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.cfg.Sources = s.cfg.Sources[:1] // только сервер
	s.find = func(context.Context, string, string, string) (kbserver.Result, error) {
		return result(
			json.RawMessage(`{"id":"<img src=x onerror=alert(1)>","title":"a"}`),
			json.RawMessage(`{"id":5,"title":{"x":1}}`),
			json.RawMessage(`{"id":6,"title":"ok","url":"javascript:alert(1)","extra":"<script>"}`),
			json.RawMessage(`{"id":7,"title":"норм","url":"https://example.com/7","verdict":"keep","description":"d"}`),
		), nil
	}
	r := s.SearchSources(context.Background(), "агент")
	if len(r.Hits) != 2 || r.Sources[0].Found != 2 {
		t.Fatalf("hits = %d, sources = %+v", len(r.Hits), r.Sources)
	}
	var first, second map[string]any
	_ = json.Unmarshal(r.Hits[0].Entry, &first)
	_ = json.Unmarshal(r.Hits[1].Entry, &second)
	if first["id"] != float64(6) || first["url"] != nil || first["extra"] != nil {
		t.Fatalf("первая запись = %v", first)
	}
	if second["url"] != "https://example.com/7" || second["verdict"] != "keep" || second["description"] != "d" {
		t.Fatalf("вторая запись = %v", second)
	}
}

// Пока окно ждет Связку, адрес сервера сменили с новым токеном: поиск по
// снимку старого конфига не отправляет новый токен на старый адрес.
func TestSearchSources_NewTokenNeverGoesToOldURL(t *testing.T) {
	s, tok, calls := newSearchSettings(t)
	s.tokens = &hookTokens{fakeTokens: tok, onFirstGet: func() {
		list := slices.Clone(s.cfg.Sources)
		list[0].URL = "https://new.example.ru"
		if err := s.SetSources(list, nil, map[string]string{"server-a": "new-token-0123456789abcdefghij"}); err != nil {
			t.Errorf("SetSources: %v", err)
		}
	}}
	s.SearchSources(context.Background(), "агент")
	for _, c := range *calls {
		if c.url == "https://kb.example.ru" && c.token == "new-token-0123456789abcdefghij" {
			t.Fatalf("новый токен ушел на старый адрес: %+v", *calls)
		}
	}
}

// hookTokens runs onFirstGet once, in the middle of the first read — «пока
// окно ждало Связку».
type hookTokens struct {
	*fakeTokens
	onFirstGet func()
	done       bool
}

func (h *hookTokens) Get(acct string) (string, error) {
	if !h.done {
		h.done = true
		h.onFirstGet()
	}
	return h.fakeTokens.Get(acct)
}

// Чтение из Связки, начатое до удаления, не возвращает удаленный токен в
// кэш: иначе он ушел бы новому хосту до перезапуска окна.
func TestCachedTokens_ReadRacingDelete(t *testing.T) {
	inner := &blockingTokens{fakeTokens: fakeTokens{m: map[string]string{"a": "old"}}, release: make(chan struct{}), started: make(chan struct{})}
	c := newCachedTokens(inner)
	done := make(chan struct{})
	go func() { _, _ = c.Get("a"); close(done) }()
	<-inner.started
	if err := c.Delete("a"); err != nil { // пока первое чтение еще идет
		t.Fatal(err)
	}
	close(inner.release)
	<-done
	if v, err := c.Get("a"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("после удаления в кэше %q, %v", v, err)
	}
}

func (b *blockingTokens) Delete(acct string) error {
	b.mu.Lock()
	delete(b.m, acct)
	b.mu.Unlock()
	return nil
}

// blockingTokens holds the first Get until release.
type blockingTokens struct {
	fakeTokens
	mu      sync.Mutex
	release chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *blockingTokens) Get(acct string) (string, error) {
	b.mu.Lock()
	v, ok := b.m[acct]
	b.mu.Unlock()
	first := false
	b.once.Do(func() { first = true; close(b.started) })
	if first {
		<-b.release
	}
	if !ok {
		return "", keychain.ErrNotFound
	}
	return v, nil
}

// Связка, которая не отвечает (ждет разблокировки), не держит выдачу: каталог
// приходит в срок, сервер назван «Связка ключей не ответила».
func TestSearchSources_HungKeychainDoesNotHoldResults(t *testing.T) {
	s, _, calls := newSearchSettings(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.tokens = hangingTokens{release}
	old := searchTimeout
	searchTimeout = 50 * time.Millisecond
	t.Cleanup(func() { searchTimeout = old })
	start := time.Now()
	r := s.SearchSources(context.Background(), "агент")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("выдача ждала Связку %v", d)
	}
	f := found(r)
	if !strings.Contains(f["server-a"].Error, "Связка ключей") || f["catalog-1"].Found != 2 || len(*calls) != 0 {
		t.Fatalf("sources = %+v, calls = %d", r.Sources, len(*calls))
	}
}

// hangingTokens never answers Get until release.
type hangingTokens struct{ release chan struct{} }

func (h hangingTokens) Get(string) (string, error) { <-h.release; return "", keychain.ErrNotFound }
func (hangingTokens) Set(string, string) error     { return nil }
func (hangingTokens) Delete(string) error          { return nil }

// Пока Связка думает, буквы запроса не запускают новые security: одно чтение
// учетки на всех, кто ждет.
func TestCachedTokens_OneReadForConcurrentGets(t *testing.T) {
	inner := &blockingTokens{fakeTokens: fakeTokens{m: map[string]string{"a": "t"}}, release: make(chan struct{}), started: make(chan struct{})}
	counted := &countingBlocking{blockingTokens: inner}
	c := newCachedTokens(counted)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if v, err := c.Get("a"); v != "t" || err != nil {
				t.Errorf("Get = %q, %v", v, err)
			}
		})
	}
	<-inner.started
	time.Sleep(20 * time.Millisecond) // остальные двое успевают встать в ожидание
	close(inner.release)
	wg.Wait()
	if n := counted.gets.Load(); n != 1 {
		t.Fatalf("Связка прочитана %d раз, want 1", n)
	}
}

type countingBlocking struct {
	*blockingTokens
	gets atomic.Int32
}

func (c *countingBlocking) Get(acct string) (string, error) {
	c.gets.Add(1)
	return c.blockingTokens.Get(acct)
}

// Склейка видит все записи источника выше, а не первые searchLimit: статья,
// что у каталога 50-я, у сервера не показывается заново.
func TestSearchSources_DedupBeyondLimit(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range 50 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":` + strconv.Itoa(i+1) + `,"title":"Агент ` + strconv.Itoa(i) + `","url":"https://example.com/k` + strconv.Itoa(i) + `","category":"ai","status":"keep","lifecycle":"active"}`)
	}
	b.WriteString(`]}`)
	if err := os.WriteFile(filepath.Join(s.home, "catalog.json"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cfg.Sources[0], s.cfg.Sources[1] = s.cfg.Sources[1], s.cfg.Sources[0] // каталог выше сервера
	s.find = func(context.Context, string, string, string) (kbserver.Result, error) {
		return result(json.RawMessage(`{"id":900,"title":"та же статья","url":"https://example.com/k49"}`)), nil
	}
	r := s.SearchSources(context.Background(), "агент")
	for _, h := range r.Hits {
		if h.Source == "server-a" {
			t.Fatalf("дубль 50-й записи каталога показан у сервера: %s", h.Entry)
		}
	}
	if r.Dupes != 1 {
		t.Fatalf("dupes = %d, want 1", r.Dupes)
	}
}

// Связка не читается под замком настроек: запертая Связка не должна вешать
// вместе с поиском и панель источников, и сохранение.
func TestSearchSources_KeychainNotReadUnderSettingsLock(t *testing.T) {
	s, tok, _ := newSearchSettings(t)
	s.tokens = lockProbe{fakeTokens: tok, s: s, t: t}
	s.SearchSources(context.Background(), "агент")
}

type lockProbe struct {
	*fakeTokens
	s *folderSettings
	t *testing.T
}

func (l lockProbe) Get(acct string) (string, error) {
	if !l.s.mu.TryLock() {
		l.t.Errorf("Связка читается под f.mu")
	} else {
		l.s.mu.Unlock()
	}
	return l.fakeTokens.Get(acct)
}

// Каждый включенный каталог проверяется при сохранении, а не только первый:
// поиск читает их все, и опечатка во втором иначе всплыла бы бедой на каждой
// букве.
func TestSetSources_ChecksEveryCatalog(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	list := []desktopconfig.Source{
		src("catalog-1", desktopconfig.KindCatalog, filepath.Join(home, "catalog.json"), true),
		src("catalog-2", desktopconfig.KindCatalog, filepath.Join(home, "catalgo.json"), true),
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
	}
	if err := s.SetSources(list, nil, nil); !errors.Is(err, errBadCatalog) {
		t.Fatalf("err = %v, want errBadCatalog", err)
	}
	list[1].Path = filepath.Join(home, "notes") // папка вместо файла
	if err := s.SetSources(list, nil, nil); !errors.Is(err, errBadCatalog) {
		t.Fatalf("папка: err = %v, want errBadCatalog", err)
	}
}

// Get, начатый ПОСЛЕ удаления, не встает в ожидание чтения, начатого до него:
// иначе он получил бы удаленный токен из того старого чтения.
func TestCachedTokens_GetAfterDeleteDoesNotJoinOldRead(t *testing.T) {
	inner := &blockingTokens{fakeTokens: fakeTokens{m: map[string]string{"a": "old"}}, release: make(chan struct{}), started: make(chan struct{})}
	c := newCachedTokens(inner)
	go func() { _, _ = c.Get("a") }()
	<-inner.started
	if err := c.Delete("a"); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() { v, _ := c.Get("a"); got <- v }()
	select {
	case v := <-got:
		if v != "" {
			t.Fatalf("после удаления Get вернул %q", v)
		}
	case <-time.After(time.Second):
		t.Fatal("Get после удаления ждет старое чтение")
	}
	close(inner.release)
}

// Сервер с limit присылает первые записи и ключи остальных: запись нижнего
// источника, что у сервера стоит за пределом присланного, склеивается по
// ключу, а «найдено» у сервера — полный счет.
func TestSearchSources_ServerKeysMerge(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.find = func(context.Context, string, string, string) (kbserver.Result, error) {
		return kbserver.Result{
			Entries: []json.RawMessage{json.RawMessage(`{"id":9,"title":"Агент сервера"}`)},
			Total:   3,
			Keys:    []string{sourcesearch.EntryKey(0, "", "https://example.com/b"), "url другое"},
		}, nil
	}
	r := s.SearchSources(context.Background(), "агент")
	if got := hitIDs(t, r); !slices.Equal(got, []string{"server-a:9", "catalog-1:1"}) {
		t.Fatalf("hits = %q: запись каталога 2 есть у сервера ключом и должна склеиться", got)
	}
	if f := found(r); f["server-a"].Found != 3 || r.Dupes != 1 {
		t.Fatalf("sources = %+v, dupes = %d", r.Sources, r.Dupes)
	}
}

// Ожидание Связки не съедает срок сети: человек разблокировал Связку под
// конец срока — сервер все равно получает свой полный срок, а не остаток.
func TestSearchSources_KeychainWaitDoesNotEatNetworkBudget(t *testing.T) {
	s, tok, _ := newSearchSettings(t)
	old := searchTimeout
	searchTimeout = 200 * time.Millisecond
	t.Cleanup(func() { searchTimeout = old })
	s.tokens = slowTokens{fakeTokens: tok, delay: 150 * time.Millisecond}
	var left time.Duration
	s.find = func(ctx context.Context, _, _, _ string) (kbserver.Result, error) {
		if d, ok := ctx.Deadline(); ok {
			left = time.Until(d)
		}
		return kbserver.Result{}, nil
	}
	s.SearchSources(context.Background(), "агент")
	if left < 150*time.Millisecond {
		t.Fatalf("серверу осталось %v из %v: срок съела Связка", left, searchTimeout)
	}
}

type slowTokens struct {
	*fakeTokens
	delay time.Duration
}

func (s slowTokens) Get(acct string) (string, error) {
	time.Sleep(s.delay)
	return s.fakeTokens.Get(acct)
}

// Отказ сохранения до записи токенов (опечатка в папке) ничего не меняет — и
// поиск, что шел в это время, не получает ложного «настройки изменились».
func TestSearchSources_RejectedSaveDoesNotCancelSearch(t *testing.T) {
	s, tok, calls := newSearchSettings(t)
	s.tokens = &hookTokens{fakeTokens: tok, onFirstGet: func() {
		bad := append(slices.Clone(s.cfg.Sources), src("folder-x", desktopconfig.KindFolder, "~/нет-такой-папки", true))
		if err := s.SetSources(bad, nil, nil); err == nil {
			t.Error("SetSources принял несуществующую папку")
		}
	}}
	r := s.SearchSources(context.Background(), "агент")
	if f := found(r); f["server-a"].Error != "" || len(*calls) != 1 {
		t.Fatalf("server-a = %+v, calls = %d", f["server-a"], len(*calls))
	}
}

// Адрес записи сервера с пробелами вокруг обрезается: и в показе, и в ключе
// склейки — запись каталога с тем же адресом не показывается дважды.
func TestSearchSources_ServerURLTrimmed(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.find = func(context.Context, string, string, string) (kbserver.Result, error) {
		return result(json.RawMessage(`{"id":1,"title":"Агент общий","url":"  https://example.com/a  "}`)), nil
	}
	r := s.SearchSources(context.Background(), "агент")
	if got := hitIDs(t, r); !slices.Equal(got, []string{"server-a:1", "catalog-1:2"}) {
		t.Fatalf("hits = %q", got)
	}
	var e map[string]any
	_ = json.Unmarshal(r.Hits[0].Entry, &e)
	if e["url"] != "https://example.com/a" {
		t.Fatalf("url = %q", e["url"])
	}
}

// Сохраненный каталог узнается по раскрытому пути: «~/catalog.json» и тот же
// путь целиком — один каталог, и сетевой диск без связи не мешает сохранению.
func TestSetSources_SavedCatalogByExpandedPath(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	s.cfg.Sources = []desktopconfig.Source{
		src("catalog-1", desktopconfig.KindCatalog, "~/offline/catalog.json", true),
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
	}
	list := slices.Clone(s.cfg.Sources)
	list[0].Path = filepath.Join(home, "offline", "catalog.json")
	if err := s.SetSources(list, nil, nil); err != nil {
		t.Fatalf("тот же каталог полным путем: %v", err)
	}
}

// Режим -browser отдает весь API и интерфейс только этой машине, а не один
// маршрут: перепривязанная страница не читает и /api/entries.
func TestBrowserHandler_LocalHostOnly(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("api")) })
	h := browserHandler(api, fstest.MapFS{"index.html": {Data: []byte("ui")}})
	for _, tt := range []struct {
		host, path string
		want       int
	}{
		{"evil.example:18090", "/api/entries", http.StatusForbidden},
		{"evil.example:18090", "/", http.StatusForbidden},
		{"127.0.0.1:18090", "/api/entries", http.StatusOK},
		{"127.0.0.1:18090", "/", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%s %s: code %d, want %d", tt.host, tt.path, rec.Code, tt.want)
		}
	}
}

// Сохранение, что держит f.mu (Связка ждет разблокировки при записи токена),
// не держит поиск: снимок настроек читается без f.mu.
func TestSearchSources_DoesNotWaitForSave(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.publish()
	s.mu.Lock() // SetSources в работе
	defer s.mu.Unlock()
	done := make(chan sourcesSearch, 1)
	go func() { done <- s.SearchSources(context.Background(), "агент") }()
	select {
	case r := <-done:
		if found(r)["catalog-1"].Found != 2 {
			t.Fatalf("sources = %+v", r.Sources)
		}
	case <-time.After(time.Second):
		t.Fatal("поиск ждет сохранения настроек")
	}
}

// Отказ из-за токена чужого id — до записи в Связку, ничего не меняет и
// поиск не отменяет.
func TestSearchSources_BadTokenSaveDoesNotCancelSearch(t *testing.T) {
	s, tok, calls := newSearchSettings(t)
	s.tokens = &hookTokens{fakeTokens: tok, onFirstGet: func() {
		if err := s.SetSources(slices.Clone(s.cfg.Sources), nil, map[string]string{"server-нет": "x"}); !errors.Is(err, errBadToken) {
			t.Errorf("err = %v, want errBadToken", err)
		}
	}}
	r := s.SearchSources(context.Background(), "агент")
	if f := found(r); f["server-a"].Error != "" || len(*calls) != 1 {
		t.Fatalf("server-a = %+v, calls = %d", f["server-a"], len(*calls))
	}
}

// Выключенный каталог, сохраненный когда-то без проверки, при включении
// проверяется: снисхождение «сетевой диск без связи» — только включенным.
func TestSetSources_DisabledSavedCatalogIsChecked(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	s.cfg.Sources = []desktopconfig.Source{
		src("catalog-1", desktopconfig.KindCatalog, filepath.Join(home, "catalog.json"), true),
		src("catalog-2", desktopconfig.KindCatalog, "~/catalgo.json", false),
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
	}
	list := slices.Clone(s.cfg.Sources)
	list[1].Enabled = true
	if err := s.SetSources(list, nil, nil); !errors.Is(err, errBadCatalog) {
		t.Fatalf("err = %v, want errBadCatalog", err)
	}
}

// Пока сохранение пишет токены (снимок «сохраняется»), серверы в поиске
// пропускаются: Связка может уже нести новый токен при старом адресе снимка.
// Каталоги отвечают как обычно.
func TestSearchSources_SkipsServersWhileSaving(t *testing.T) {
	s, _, calls := newSearchSettings(t)
	s.mu.Lock()
	s.publishLocked(true)
	s.mu.Unlock()
	r := s.SearchSources(context.Background(), "агент")
	f := found(r)
	if f["server-a"].Error != errSourceChanged || f["catalog-1"].Found != 2 || len(*calls) != 0 {
		t.Fatalf("sources = %+v, calls = %d", r.Sources, len(*calls))
	}
}

// Окно просит у сервера вдвое больше, чем покажет: первые записи сервера
// часто дублируют источник выше и склеиваются, и без запаса от сервера
// осталась бы горстка строк при сотнях найденных.
func TestFindServer_AsksWithMargin(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	if _, err := findServer(context.Background(), srv.URL, "", "q"); err != nil {
		t.Fatal(err)
	}
	if got != strconv.Itoa(2*searchLimit) {
		t.Fatalf("limit = %q, want %d", got, 2*searchLimit)
	}
}

// Ключ показанной записи сервера — по исходному адресу, как ключи остальных
// записей, что сервер считает сам: иначе склейка зависела бы от того, попала
// запись в первые N или нет.
func TestTypedEntries_KeyFromRawURL(t *testing.T) {
	items := typedEntries([]json.RawMessage{json.RawMessage(`{"id":5,"title":"Канал","url":"t.me/chan/5"}`)})
	if len(items) != 1 || items[0].key != sourcesearch.EntryKey(5, "Канал", "t.me/chan/5") {
		t.Fatalf("items = %+v", items)
	}
}

// Запись своего каталога приходит с признаком находки: «агент» совпал в
// заголовке буквально.
func TestSearchSources_CatalogHitCarriesMatch(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.cfg.Sources = s.cfg.Sources[1:] // только каталог
	r := s.SearchSources(context.Background(), "агент")
	var e struct {
		Match []struct{ Field, How, Hit string } `json:"match"`
	}
	if err := json.Unmarshal(r.Hits[0].Entry, &e); err != nil || len(e.Match) != 1 || e.Match[0].Field != "title" || e.Match[0].How != "exact" || e.Match[0].Hit != "Агент" {
		t.Fatalf("entry = %s", r.Hits[0].Entry)
	}
}

// Признак находки чужого сервера разбирается строго, как и запись: известные
// поле и способ проходят, неизвестные — отброшены, запись остается.
func TestSearchSources_ServerMatchIsTyped(t *testing.T) {
	s, _, _ := newSearchSettings(t)
	s.cfg.Sources = s.cfg.Sources[:1]
	s.find = func(context.Context, string, string, string) (kbserver.Result, error) {
		return result(json.RawMessage(`{"id":9,"title":"Агент","match":[
			{"word":"жо","field":"author","text":"zhogov1985","hit":"zhogov1985","how":"translit"},
			{"word":"x","field":"<img>","text":"t","hit":"t","how":"exact"},
			{"word":"y","field":"title","text":"t","hit":"t","how":"<script>"}]}`)), nil
	}
	r := s.SearchSources(context.Background(), "жо")
	var e struct {
		Match []struct{ Word, Field, How string } `json:"match"`
	}
	if err := json.Unmarshal(r.Hits[0].Entry, &e); err != nil || len(e.Match) != 1 || e.Match[0].Field != "author" || e.Match[0].How != "translit" {
		t.Fatalf("entry = %s", r.Hits[0].Entry)
	}
}
