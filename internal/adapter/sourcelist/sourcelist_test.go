package sourcelist

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// Включенные источники известных видов — в порядке списка; выключенный и
// неизвестного вида (из более новой сборки) не идут.
func TestEnabled(t *testing.T) {
	cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "server-a", Kind: desktopconfig.KindServer, URL: "https://a.example.ru", Enabled: true},
		{ID: "catalog-b", Kind: desktopconfig.KindCatalog, Path: "/c.json", Enabled: false},
		{ID: "folder-c", Kind: desktopconfig.KindFolder, Path: "/n", Enabled: true},
		{ID: "x-d", Kind: "future", Enabled: true},
		{ID: "catalog-e", Kind: desktopconfig.KindCatalog, Path: "/e.json", Enabled: true},
	}}
	var ids []string
	for _, s := range Enabled(cfg) {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "server-a,folder-c,catalog-e" {
		t.Fatalf("ids = %v", ids)
	}
}

// Связка, ждущая разблокировки, не держит ответ: срок вышел — ошибка ctx.
func TestTokenWithin(t *testing.T) {
	tests := []struct {
		name    string
		get     func(string) (string, error)
		want    string
		wantErr error
	}{
		{"ответила", func(id string) (string, error) { return "tok-" + id, nil }, "tok-a", nil},
		{"записи нет", func(string) (string, error) { return "", errNone }, "", errNone},
		{"зависла", func(string) (string, error) { time.Sleep(time.Second); return "late", nil }, "", context.DeadlineExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			got, err := TokenWithin(ctx, tc.get, "a")
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %q, %v; want %q, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

var errNone = errors.New("нет записи")

// Запись чужого сервера разбирается строго: поле не того типа или без
// заголовка — отброшена; адрес не http(s) — убран; ключ склейки — по исходному
// адресу; признаки — только известные, не больше предела и не простыней.
func TestDecodeEntries(t *testing.T) {
	many := make([]string, 0, 40)
	for range 40 {
		many = append(many, `{"word":"a","field":"title","hit":"a","how":"exact"}`)
	}
	raw := []json.RawMessage{
		json.RawMessage(`{"id":"7","title":"номер строкой"}`),
		json.RawMessage(`{"id":8}`),
		json.RawMessage(`{"id":5,"title":"Канал","url":"t.me/chan/5"}`),
		json.RawMessage(`{"id":6,"title":"XSS","url":"javascript:alert(1)"}`),
		json.RawMessage(`{"id":9,"title":"Признак не того вида","match":{"word":"x"}}`),
		json.RawMessage(`{"id":10,"title":"Признаки","match":[
			{"word":"x","field":"<img>","hit":"t","how":"exact"},
			{"word":"y","field":"title","hit":"` + strings.Repeat("я", 300) + `","how":"exact"},
			` + strings.Join(many, ",") + `]}`),
	}
	got := DecodeEntries(raw)
	if len(got) != 4 {
		t.Fatalf("записей %d, want 4: %+v", len(got), got)
	}
	if got[0].Key != sourcesearch.EntryKey(5, "Канал", "t.me/chan/5") {
		t.Fatalf("ключ = %q", got[0].Key)
	}
	if got[1].Entry.URL != "" {
		t.Fatalf("javascript: адрес остался: %q", got[1].Entry.URL)
	}
	if got[2].Entry.Match != nil {
		t.Fatalf("признак не того вида: %+v", got[2].Entry.Match)
	}
	ms := got[3].Entry.Match
	if len(ms) != MaxMatches {
		t.Fatalf("признаков %d, want %d", len(ms), MaxMatches)
	}
	for _, m := range ms {
		if m.Field != "title" || len(m.Hit) > MaxMatchLen {
			t.Fatalf("признак %+v", m)
		}
	}
}

// Поиск по файлу каталога — тот же фильтр, что /api/search; файла нет — ошибка.
func TestSearchCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"entries":[
		{"id":1,"title":"Агент в продакшене","category":"ai","status":"KEEP","date_added":"2026-01-01"},
		{"id":2,"title":"Кулинария","category":"misc","status":"KEEP","date_added":"2026-01-01"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := SearchCatalog(path, "агент", search.Matcher{})
	if err != nil || len(got) != 1 || got[0].ID() != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := SearchCatalog(filepath.Join(t.TempDir(), "нет.json"), "агент", search.Matcher{}); err == nil {
		t.Fatal("нет ошибки для отсутствующего файла")
	}
}

// Ревью #402. Поля, которые окно не показывает (kind, lifecycle, tags), не того
// типа не отбрасывают запись: окно показывало ее до общего пакета. Правильные —
// доходят до MCP; в JSON окна их нет.
func TestDecodeEntries_ExtraFieldsAreLenient(t *testing.T) {
	got := DecodeEntries([]json.RawMessage{
		json.RawMessage(`{"id":1,"title":"Теги строкой","tags":"ai","kind":1}`),
		json.RawMessage(`{"id":2,"title":"Все верно","tags":["ai"],"kind":"article","lifecycle":"active"}`),
	})
	if len(got) != 2 {
		t.Fatalf("записей %d, want 2", len(got))
	}
	if e := got[1].Entry; e.Kind != "article" || e.Lifecycle != "active" || len(e.Tags) != 1 {
		t.Fatalf("поля для MCP = %+v", e)
	}
	raw, _ := json.Marshal(got[1].Entry)
	if strings.Contains(string(raw), "kind") || strings.Contains(string(raw), "tags") {
		t.Fatalf("в JSON окна лишние поля: %s", raw)
	}
}
