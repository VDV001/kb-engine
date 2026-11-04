package mcpserver_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

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
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{
		{Source: mcpserver.RemoteSource{ID: "server-a", Name: "vps"}, Total: 3, Entries: []json.RawMessage{
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
	cs := connectRemote(t, fakeRemote{found: []mcpserver.RemoteFound{
		{Source: mcpserver.RemoteSource{ID: "server-a"}, Total: 1, Keys: []string{"url example.com/z"}},
		{Source: mcpserver.RemoteSource{ID: "server-b"}, Total: 1, Entries: []json.RawMessage{
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
