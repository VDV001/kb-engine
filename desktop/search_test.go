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
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
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
	s.find = func(_ context.Context, u, token, q string) ([]json.RawMessage, error) {
		mu.Lock()
		calls = append(calls, searchCall{u, token, q})
		mu.Unlock()
		switch {
		case strings.Contains(u, "down"):
			return nil, kbserver.ErrUnreachable
		case token != srvToken:
			return nil, kbserver.ErrUnauthorized
		}
		return []json.RawMessage{
			json.RawMessage(`{"id":1,"title":"Агент общий","url":"https://example.com/a"}`),
			json.RawMessage(`{"id":9,"title":"Агент сервера"}`),
		}, nil
	}
	return s, tok, &calls
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

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/search?q="+url.QueryEscape("агент"), nil))
	var got sourcesSearch
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("code %d, err %v, body %s", rec.Code, err, rec.Body)
	}
	if len(got.Hits) != 3 || len(got.Sources) != 2 {
		t.Fatalf("body = %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/search?q=", nil))
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
	s.find = func(context.Context, string, string, string) ([]json.RawMessage, error) {
		out := []json.RawMessage{json.RawMessage(`{"id":1,"title":"Агент общий","url":"https://example.com/a"}`)}
		for i := range searchLimit + 20 {
			out = append(out, json.RawMessage(`{"id":`+strconv.Itoa(100+i)+`,"title":"с`+strconv.Itoa(i)+`"}`))
		}
		return out, nil
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
	s.find = func(context.Context, string, string, string) ([]json.RawMessage, error) {
		return []json.RawMessage{
			json.RawMessage(`{"id":"<img src=x onerror=alert(1)>","title":"a"}`),
			json.RawMessage(`{"id":5,"title":{"x":1}}`),
			json.RawMessage(`{"id":6,"title":"ok","url":"javascript:alert(1)","extra":"<script>"}`),
			json.RawMessage(`{"id":7,"title":"норм","url":"https://example.com/7","verdict":"keep","description":"d"}`),
		}, nil
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
