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
	raw := text(t, callTool(t, cs, "search_catalog", map[string]any{"query": "кубернетес"}))
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
