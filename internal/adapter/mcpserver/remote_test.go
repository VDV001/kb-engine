package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// own — свой каталог MCP (--catalog) в списке источников окна.
var own = mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: "catalog-own"}, Own: true}

// fakeRemote — kb-серверы из списка источников окна.
type fakeRemote struct{ found []mcpserver.RemoteFound }

func (f fakeRemote) SearchRemote(context.Context, string) []mcpserver.RemoteFound { return f.found }

func connectRemote(t *testing.T, r mcpserver.RemoteSearcher) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: fixture(t)}, search.Matcher{}, mcpserver.Config{Version: "тест", Remote: r})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type sourcedOut struct {
	Found   int `json:"found"`
	Entries []struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		Source string `json:"source"`
	} `json:"entries"`
	Sources []struct {
		ID    string `json:"id"`
		Found int    `json:"found"`
		Error string `json:"error"`
	} `json:"sources"`
}

// С kb-серверами из списка источников search_catalog отвечает одной выдачей:
// свой каталог первым, запись, что есть и в каталоге, и на сервере, — раз (у
// каталога); у каждой записи — источник, у каждого источника — сколько нашел
// или почему не ответил. Запись сервера не того вида отброшена.
func TestServer_searchMergesRemoteServers(t *testing.T) {
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own,
		{Source: mcpserver.RemoteSource{ID: "server-a", Name: "vps"}, Entries: []json.RawMessage{
			json.RawMessage(`{"id":1,"title":"Оркестрация контейнеров","category":"ai","kind":"article","lifecycle":"active"}`),
			json.RawMessage(`{"id":50,"title":"Kubernetes на сервере","category":"ai","kind":"article","lifecycle":"active"}`),
			json.RawMessage(`{"id":"<x>","title":"не того вида"}`),
		}},
		{Source: mcpserver.RemoteSource{ID: "server-b"}, Err: "нет связи: проверьте адрес и VPN"},
	}})
	res := callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"})
	var out sourcedOut
	if err := json.Unmarshal([]byte(text(t, res)), &out); err != nil {
		t.Fatalf("%v\n%s", err, text(t, res))
	}
	got := []string{}
	for _, e := range out.Entries {
		got = append(got, e.Source+":"+itoa(e.ID))
	}
	if want := []string{"catalog:1", "server-a:50"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("entries = %q, want %q", got, want)
	}
	if out.Found != 2 || len(out.Sources) != 3 {
		t.Fatalf("found = %d, sources = %+v", out.Found, out.Sources)
	}
	if s := out.Sources[1]; s.ID != "server-a" || s.Found != 2 || s.Error != "" {
		t.Fatalf("server-a = %+v: найдено — то, что прошло разбор", s)
	}
	if s := out.Sources[2]; s.ID != "server-b" || s.Error == "" {
		t.Fatalf("server-b = %+v", s)
	}
}

// Без серверов ответ прежний: ни поля source, ни списка sources — агенты,
// что читают поиск по каталогу, не видят разницы.
func TestServer_searchWithoutRemoteIsUnchanged(t *testing.T) {
	cs := connect(t, stubQuerier{entries: fixture(t)})
	raw := text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["sources"]; ok {
		t.Fatalf("без серверов появилось sources: %s", raw)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// Ключи остальных записей сервера участвуют в склейке: запись источника ниже,
// что у сервера выше пришла только ключом, не показывается второй раз; в
// «найдено» сервера входят и записи, известные ключом.
func TestServer_searchRemoteKeysMerge(t *testing.T) {
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own,
		{Source: mcpserver.RemoteSource{ID: "server-a"}, Keys: []string{"url example.com/z"}},
		{Source: mcpserver.RemoteSource{ID: "server-b"}, Entries: []json.RawMessage{
			json.RawMessage(`{"id":9,"title":"Та же статья","url":"https://example.com/z","category":"ai","kind":"article","lifecycle":"active"}`),
		}},
	}})
	var out sourcedOut
	if err := json.Unmarshal([]byte(text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))), &out); err != nil {
		t.Fatal(err)
	}
	for _, e := range out.Entries {
		if e.Source == "server-b" {
			t.Fatalf("дубль ключа server-a показан у server-b: %+v", out.Entries)
		}
	}
	if out.Sources[1].Found != 1 {
		t.Fatalf("server-a = %+v: ключ входит в найдено", out.Sources[1])
	}
}

// Ревью #396. Сервер 0.30.0 limit не знает и отдает все найденное: записей от
// сервера в ответе не больше предела, остальные — как ключи, в «найдено».
// Пустая выдача — [], а не null. У записи сервера нет file и related_ids: это
// пути и номера чужого каталога, get_entry и read_file открыли бы свое.
func TestServer_searchRemoteBounded(t *testing.T) {
	var many []json.RawMessage
	for i := range 100 {
		many = append(many, json.RawMessage(`{"id":`+itoa(1000+i)+`,"title":"с`+itoa(i)+`","file":"notes/x.md","related_ids":[1],"category":"ai","kind":"article","lifecycle":"active"}`))
	}
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own, {Source: mcpserver.RemoteSource{ID: "server-a"}, Entries: many}}})
	// limit большой: проверяется предел одного сервера (RemoteLimit), а не общий предел ответа.
	raw := text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес", "limit": 1000}))
	var out struct {
		Entries []map[string]any `json:"entries"`
		Sources []struct {
			ID    string `json:"id"`
			Found int    `json:"found"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	server := 0
	for _, e := range out.Entries {
		if e["source"] == "server-a" {
			server++
			if e["file"] != nil || e["related_ids"] != nil {
				t.Fatalf("у записи сервера чужие file/related_ids: %v", e)
			}
		}
	}
	if server != mcpserver.RemoteLimit || out.Sources[1].Found != 100 {
		t.Fatalf("записей сервера %d (предел %d), найдено %d", server, mcpserver.RemoteLimit, out.Sources[1].Found)
	}

	empty := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{{Source: mcpserver.RemoteSource{ID: "server-a"}}}})
	var e map[string]json.RawMessage
	_ = json.Unmarshal([]byte(text(t, callTool(t, empty, "search_catalog", map[string]any{"query": "телепортация"}))), &e)
	if string(e["entries"]) != "[]" {
		t.Fatalf("пустая выдача: entries = %s", e["entries"])
	}
}

// Новая форма ответа — только когда в списке окна есть что-то кроме своего
// каталога: с --files-config, где включен один свой каталог, ответ прежний.
func TestServer_searchRemoteWithoutServersIsUnchanged(t *testing.T) {
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own}})
	var m map[string]json.RawMessage
	_ = json.Unmarshal([]byte(text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))), &m)
	if _, ok := m["sources"]; ok {
		t.Fatal("серверов нет, а sources есть")
	}
}

func entryOf(t *testing.T, id int, title string) domain.Entry {
	t.Helper()
	return entry(t, id, title, "", "kubernetes")
}

func searchOut(t *testing.T, found []mcpserver.RemoteFound) sourcedOut {
	t.Helper()
	cs := connectRemote(t, fakeRemote{found: found})
	raw := text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))
	var out sourcedOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	return out
}

func entrySources(out sourcedOut) []string {
	got := []string{}
	for _, e := range out.Entries {
		got = append(got, e.Source+":"+itoa(e.ID))
	}
	return got
}

func sourceIDs(out sourcedOut) []string {
	got := []string{}
	for _, s := range out.Sources {
		got = append(got, s.ID)
	}
	return got
}

// Агент видит то, что окно: порядок, выключенные источники и вторые каталоги
// берутся из списка окна, а не «свой каталог всегда первым».
func TestServer_searchFollowsWindowList(t *testing.T) {
	server := mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: "server-a"}, Entries: []json.RawMessage{
		json.RawMessage(`{"id":1,"title":"Оркестрация контейнеров"}`),
		json.RawMessage(`{"id":50,"title":"Kubernetes на сервере"}`),
	}}
	second := mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: "catalog-2", Name: "работа"}, Catalog: []domain.Entry{entryOf(t, 70, "Кубернетес в работе")}}
	tests := []struct {
		name        string
		found       []mcpserver.RemoteFound
		wantEntries []string
		wantSources []string
	}{
		// Сервер выше каталога: дубль показан у сервера, а не у каталога.
		{"сервер выше каталога", []mcpserver.RemoteFound{server, own}, []string{"server-a:1", "server-a:50"}, []string{"server-a", "catalog"}},
		// Свой каталог выключен в окне — агент его не видит.
		{"каталог выключен", []mcpserver.RemoteFound{server}, []string{"server-a:1", "server-a:50"}, []string{"server-a"}},
		// Второй каталог окна отвечает своими записями под своим id.
		{"второй каталог", []mcpserver.RemoteFound{own, second}, []string{"catalog:1", "catalog-2:70"}, []string{"catalog", "catalog-2"}},
		// В окне нет ни каталогов, ни серверов — пустая выдача, а не свой каталог.
		{"источников нет", []mcpserver.RemoteFound{}, []string{}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := searchOut(t, tc.found)
			if got := entrySources(out); strings.Join(got, ",") != strings.Join(tc.wantEntries, ",") {
				t.Fatalf("entries = %q, want %q", got, tc.wantEntries)
			}
			if got := sourceIDs(out); strings.Join(got, ",") != strings.Join(tc.wantSources, ",") {
				t.Fatalf("sources = %q, want %q", got, tc.wantSources)
			}
		})
	}
}

// Ревью #402. Запись второго каталога — как запись сервера: без file и
// related_ids, это пути и номера чужого каталога, get_entry и read_file
// открыли бы по ним свое.
func TestServer_searchSecondCatalogHasNoForeignPaths(t *testing.T) {
	cat, _ := domain.NewCategory("articles")
	life, _ := domain.NewLifecycle("active")
	read, _ := domain.NewReadState("read")
	e, err := domain.NewEntry(domain.EntryParams{ID: 70, Kind: domain.KindArticle, Title: "Кубернетес в работе",
		Category: cat, Lifecycle: life, ReadState: &read, NotesFile: "notes/x.md", RelatedIDs: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own, {Source: mcpserver.RemoteSource{ID: "catalog-2"}, Catalog: []domain.Entry{e}}}})
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	_ = json.Unmarshal([]byte(text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))), &out)
	for _, x := range out.Entries {
		if x["source"] == "catalog-2" && (x["file"] != nil || x["related_ids"] != nil) {
			t.Fatalf("у записи второго каталога чужие file/related_ids: %v", x)
		}
	}
}

// Ревью #402. Пустой запрос (весь каталог) тоже идет по списку окна:
// выключенный в окне свой каталог не отдается целиком.
func TestServer_searchEmptyQueryFollowsWindowList(t *testing.T) {
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{{Source: mcpserver.RemoteSource{ID: "server-a"}}}})
	var out sourcedOut
	_ = json.Unmarshal([]byte(text(t, callTool(t, cs, "search_catalog", map[string]any{"query": ""}))), &out)
	if len(out.Entries) != 0 || strings.Join(sourceIDs(out), ",") != "server-a" {
		t.Fatalf("entries = %v, sources = %v", entrySources(out), sourceIDs(out))
	}
}

// Ревью #402. Свой каталог, добавленный в окно дважды, отвечает раз: иначе
// агент сложил бы два одинаковых «найдено».
func TestServer_searchOwnCatalogTwiceAnswersOnce(t *testing.T) {
	out := searchOut(t, []mcpserver.RemoteFound{own, {Source: mcpserver.RemoteSource{ID: "server-a"}}, own})
	if got := strings.Join(sourceIDs(out), ","); got != "catalog,server-a" {
		t.Fatalf("sources = %s", got)
	}
}

// Запись сервера приходит агенту с признаком находки, как в окне («совпало:
// автор · жо → zhogov1985 · транслит»), а поле, которого сервер не прислал,
// в ответе отсутствует, а не пусто: «неизвестно» и «пустая категория» у
// агента не должны выглядеть одинаково.
func TestServer_searchRemoteEntryCarriesMatch(t *testing.T) {
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own,
		{Source: mcpserver.RemoteSource{ID: "server-a"}, Entries: []json.RawMessage{
			json.RawMessage(`{"id":50,"title":"Kubernetes на сервере","category":"ai","kind":"article","lifecycle":"active",` +
				`"match":[{"word":"кубернетес","field":"title","hit":"Kubernetes","how":"translit"}]}`),
			json.RawMessage(`{"id":51,"title":"Kubernetes без полей"}`),
		}},
	}})
	raw := text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))
	var out struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, e := range out.Entries {
		byID[string(e["source"])+":"+string(e["id"])] = e
	}
	full, bare := byID[`"server-a":50`], byID[`"server-a":51`]
	if full == nil || bare == nil {
		t.Fatalf("нет записей сервера в ответе: %s", raw)
	}
	var ms []struct{ Word, Field, Hit, How string }
	if err := json.Unmarshal(full["match"], &ms); err != nil || len(ms) != 1 ||
		ms[0].Word != "кубернетес" || ms[0].Field != "title" || ms[0].Hit != "Kubernetes" || ms[0].How != "translit" {
		t.Fatalf("match = %s, want признак сервера как есть", full["match"])
	}
	for _, k := range []string{"category", "kind", "lifecycle", "match"} {
		if v, ok := bare[k]; ok {
			t.Errorf("запись без %s отдала %s=%s: поля нет — его нет и в ответе", k, k, v)
		}
	}
}

// Признак находки есть у записи любого каталога, как в окне (desktop
// searchCatalog объясняет каждую показанную): у своего без списка окна, у
// своего в списке и у второго каталога. Агент видит, почему запись нашлась,
// не открывая витрину.
func TestServer_searchCatalogEntryCarriesMatch(t *testing.T) {
	cat, _ := domain.NewCategory("articles")
	life, _ := domain.NewLifecycle("active")
	read, _ := domain.NewReadState("read")
	second, err := domain.NewEntry(domain.EntryParams{ID: 70, Kind: domain.KindArticle, Title: "Кубернетес в работе",
		Category: cat, Lifecycle: life, ReadState: &read})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source string
		id           int
		cs           func(*testing.T) *mcp.ClientSession
	}{
		{"свой без списка окна", "", 1, func(t *testing.T) *mcp.ClientSession { return connect(t, stubQuerier{entries: fixture(t)}) }},
		{"свой в списке окна", "catalog", 1, func(t *testing.T) *mcp.ClientSession {
			return connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own, {Source: mcpserver.RemoteSource{ID: "server-a"}}}})
		}},
		{"второй каталог", "catalog-2", 70, func(t *testing.T) *mcp.ClientSession {
			return connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{own,
				{Source: mcpserver.RemoteSource{ID: "catalog-2"}, Catalog: []domain.Entry{second}}}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := text(t, callTool(t, tc.cs(t), "search_catalog", map[string]any{"query": "кубернетес"}))
			var out struct {
				Entries []struct {
					ID     int    `json:"id"`
					Source string `json:"source"`
					Match  []struct {
						Word string `json:"word"`
					} `json:"match"`
				} `json:"entries"`
			}
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			for _, e := range out.Entries {
				if e.ID == tc.id && e.Source == tc.source {
					if len(e.Match) == 0 || e.Match[0].Word != "кубернетес" {
						t.Fatalf("match = %+v, want признак слова «кубернетес»\n%s", e.Match, raw)
					}
					return
				}
			}
			t.Fatalf("нет записи %s:%d в ответе\n%s", tc.source, tc.id, raw)
		})
	}
}
