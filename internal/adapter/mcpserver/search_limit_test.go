package mcpserver_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
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
