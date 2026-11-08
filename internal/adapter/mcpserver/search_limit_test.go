package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// manyEntries — n записей, у которых общее слово «агент» в заголовке: широкий
// запрос, который в живом каталоге (1620 записей) отдавал сотни карточек.
func manyEntries(t *testing.T, n int) []domain.Entry {
	t.Helper()
	out := make([]domain.Entry, n)
	for i := range out {
		out[i] = entry(t, 100+i, fmt.Sprintf("Агент номер %d", i), "описание")
	}
	return out
}

type limitedAnswer struct {
	Found   int               `json:"found"`
	Total   *int              `json:"total"`
	More    *bool             `json:"more"`
	Entries []json.RawMessage `json:"entries"`
}

func searchLimited(t *testing.T, n int, args map[string]any) (limitedAnswer, string) {
	t.Helper()
	cs := connect(t, stubQuerier{entries: manyEntries(t, n)})
	raw := text(t, callTool(t, cs, "search_catalog", args))
	var out limitedAnswer
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("ответ не разбирается: %v\n%s", err, raw)
	}
	return out, raw
}

// Широкий запрос без limit отдаёт не больше 20 записей и говорит, что нашлось
// больше: замер 08.11 — «агент» в живом каталоге давал 652 карточки, сотни
// тысяч токенов за один вызов, и они же оставались в контексте.
func TestServer_searchLimitsTheAnswerByDefault(t *testing.T) {
	out, _ := searchLimited(t, 30, map[string]any{"query": "агент"})
	if len(out.Entries) != 20 || out.Found != 20 {
		t.Fatalf("в ответе %d записей, found %d, ждали 20", len(out.Entries), out.Found)
	}
	if out.Total == nil || *out.Total != 30 || out.More == nil || !*out.More {
		t.Fatalf("о том, что нашлось 30, ответ не сказал: total=%v more=%v", out.Total, out.More)
	}
}

// limit задает размер явно; когда все влезло, признаков усечения нет: «more»
// у полного ответа сбил бы агента искать то, чего нет.
func TestServer_searchLimitArgument(t *testing.T) {
	cases := []struct {
		name      string
		limit     int
		wantShown int
		wantMore  bool
	}{
		{"меньше найденного", 5, 5, true},
		{"ровно столько, сколько найдено", 30, 30, false},
		{"больше найденного", 500, 30, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := searchLimited(t, 30, map[string]any{"query": "агент", "limit": tc.limit})
			if len(out.Entries) != tc.wantShown || out.Found != tc.wantShown {
				t.Fatalf("limit %d: записей %d, found %d, ждали %d", tc.limit, len(out.Entries), out.Found, tc.wantShown)
			}
			if tc.wantMore != (out.More != nil && *out.More) || tc.wantMore != (out.Total != nil) {
				t.Fatalf("limit %d: признаки усечения total=%v more=%v, ждали усечение=%v", tc.limit, out.Total, out.More, tc.wantMore)
			}
		})
	}
}

// Ответ агенту — одна строка JSON: отступы стоили 24% токенов на том же
// содержимом (замер 08.11: 67 880 против 51 490).
func TestServer_answersAreCompactJSON(t *testing.T) {
	_, raw := searchLimited(t, 3, map[string]any{"query": "агент"})
	if strings.Contains(raw, "\n") {
		t.Fatalf("ответ с переводами строк и отступами:\n%.200s", raw)
	}
}

// connectManyRemote — свой каталог на n записей «Агент номер N» и kb-серверы
// из списка окна: ветка с несколькими источниками, где лимит делили общий.
func connectManyRemote(t *testing.T, n int, remote ...mcpserver.RemoteFound) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcpserver.New(stubQuerier{entries: manyEntries(t, n)}, search.Matcher{},
		mcpserver.Config{Version: "тест", Remote: fakeRemote{found: append([]mcpserver.RemoteFound{own}, remote...)}})
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

func serverEntries(n int) []json.RawMessage {
	out := make([]json.RawMessage, n)
	for i := range out {
		out[i] = json.RawMessage(fmt.Sprintf(`{"id":%d,"title":"Агент сервер %d","category":"ai","kind":"article","lifecycle":"active"}`, 5000+i, i))
	}
	return out
}

type multiAnswer struct {
	Found   int   `json:"found"`
	Total   *int  `json:"total"`
	More    *bool `json:"more"`
	Entries []struct {
		Source string `json:"source"`
	} `json:"entries"`
}

func askMulti(t *testing.T, cs *mcp.ClientSession, args map[string]any) multiAnswer {
	t.Helper()
	var out multiAnswer
	if err := json.Unmarshal([]byte(text(t, callTool(t, cs, "search_catalog", args))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func perSource(a multiAnswer) map[string]int {
	got := map[string]int{}
	for _, e := range a.Entries {
		got[e.Source]++
	}
	return got
}

// Ревью #519: лимит делился между источниками общий, и свой каталог (первый в
// списке, без предела) забирал все места: kb-сервер из окна не показывал ни
// одной записи, а more об этом молчал. Лимит теперь у каждого источника свой.
func TestServer_searchLimitIsPerSource(t *testing.T) {
	cs := connectManyRemote(t, 30, mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: "server-a"}, Entries: serverEntries(5)})
	out := askMulti(t, cs, map[string]any{"query": "агент"})
	got := perSource(out)
	if got["catalog"] != 20 || got["server-a"] != 5 {
		t.Fatalf("по источникам %v, ждали catalog 20 и server-a 5", got)
	}
	if out.Total == nil || *out.Total != 35 || out.More == nil || !*out.More {
		t.Fatalf("total=%v more=%v, ждали total 35 и more (свой каталог усечен)", out.Total, out.More)
	}
}

// total — сколько нашли источники, а не сколько осталось после предела сервера
// (RemoteLimit): иначе 300 найденных при 40 показанных выглядели полным ответом.
func TestServer_searchTotalCountsWhatTheServerLimitCut(t *testing.T) {
	cs := connectManyRemote(t, 3, mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: "server-a"}, Entries: serverEntries(100)})
	out := askMulti(t, cs, map[string]any{"query": "агент", "limit": 50})
	if got := perSource(out)["server-a"]; got != mcpserver.RemoteLimit {
		t.Fatalf("записей сервера %d, ждали предел %d", got, mcpserver.RemoteLimit)
	}
	if out.Total == nil || *out.Total != 103 || out.More == nil || !*out.More {
		t.Fatalf("total=%v more=%v, ждали total 103 и more: часть записей сервера не показана", out.Total, out.More)
	}
}

// Потолок limit: агент не вытягивает весь каталог одним числом (решение
// владельца 08.11, ревью #519); весь каталог — витрина и терминал, не MCP.
func TestServer_searchLimitHasACeiling(t *testing.T) {
	out, _ := searchLimited(t, 250, map[string]any{"query": "агент", "limit": 100000})
	if len(out.Entries) != mcpserver.MaxSearchLimit {
		t.Fatalf("записей %d, потолок %d", len(out.Entries), mcpserver.MaxSearchLimit)
	}
	if out.Total == nil || *out.Total != 250 || out.More == nil || !*out.More {
		t.Fatalf("total=%v more=%v, ждали total 250 и more", out.Total, out.More)
	}
}
