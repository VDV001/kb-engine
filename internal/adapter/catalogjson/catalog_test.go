package catalogjson_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/domain"
)

// articleJSON builds a one-entry catalog whose article has the given raw status.
func articleJSON(status string) string {
	return `{"entries":[{"id":1,"habr_id":1049782,"title":"T",` +
		`"url":"https://habr.com/x/","category":"ai-agents-tools",` +
		`"status":"` + status + `","lifecycle":"active"}]}`
}

func TestDecode_articleStatusMapping(t *testing.T) {
	tests := []struct {
		name          string
		status        string
		wantVerdict   string // "" = none
		wantReadState string
	}{
		{"keep", "keep", "keep", "read"},
		{"uppercase KEEP normalized", "KEEP", "keep", "read"},
		{"cyrillic legacy normalized", "на подумать", "consider", "read"},
		{"transliterated legacy normalized", "napodumat", "consider", "read"},
		{"skip", "skip", "skip", "read"},
		{"skip-unavailable", "SKIP-unavailable", "skip-unavailable", "read"},
		{"read without verdict", "read", "", "read"},
		{"unread without verdict", "unread", "", "unread"},
		// Some legacy entries echo the lifecycle into the status field: catalog
		// id=1312 stores status="active" next to lifecycle="active". That is
		// storage noise, not triage — the entry was looked at, but no verdict was
		// ever recorded. Decode it as read-without-verdict instead of failing the
		// whole catalog on one malformed field.
		{"lifecycle echo: active", "active", "", "read"},
		{"lifecycle echo: canonical", "canonical", "", "read"},
		{"lifecycle echo: outdated", "outdated", "", "read"},
		{"lifecycle echo: superseded", "superseded", "", "read"},
		{"lifecycle echo: dead-end", "dead-end", "", "read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalogjson.Decode(strings.NewReader(articleJSON(tt.status)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			e, ok := c.Find(1)
			if !ok {
				t.Fatal("entry 1 not found")
			}
			if e.Kind() != "article" {
				t.Errorf("kind = %q, want article", e.Kind())
			}
			if tt.wantVerdict == "" {
				if e.Verdict() != nil {
					t.Errorf("verdict = %v, want nil", e.Verdict())
				}
			} else if e.Verdict() == nil || e.Verdict().String() != tt.wantVerdict {
				t.Errorf("verdict = %v, want %q", e.Verdict(), tt.wantVerdict)
			}
			if e.ReadState() == nil || e.ReadState().String() != tt.wantReadState {
				t.Errorf("read state = %v, want %q", e.ReadState(), tt.wantReadState)
			}
		})
	}
}

func TestDecode_habrIDAcceptsStringOrNumber(t *testing.T) {
	tests := []struct {
		name string
		raw  string // value of the habr_id field, verbatim JSON
		want *int
	}{
		{"number", `1049782`, new(1049782)},
		{"numeric string", `"1049782"`, new(1049782)},
		{"null", `null`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := `{"entries":[{"id":1,"habr_id":` + tt.raw +
				`,"title":"T","url":"https://h/","category":"golang","status":"read"}]}`
			c, err := catalogjson.Decode(strings.NewReader(src))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			e, _ := c.Find(1)
			switch {
			case tt.want == nil && e.HabrID() != nil:
				t.Errorf("habrID = %v, want nil", *e.HabrID())
			case tt.want != nil && (e.HabrID() == nil || *e.HabrID() != *tt.want):
				t.Errorf("habrID = %v, want %d", e.HabrID(), *tt.want)
			}
		})
	}
}

func TestDecode_metadataFields(t *testing.T) {
	src := `{"entries":[{"id":1,"habr_id":1,"title":"T","url":"https://h/",` +
		`"category":"golang","status":"keep","source":"bot-inbox","author":"A",` +
		`"notes":"a note","supersedes_id":42,"related_ids":[2,"3"],` +
		`"date_added":"2026-04-24","date_created":"2026-03-24"}]}`
	c, err := catalogjson.Decode(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	e, _ := c.Find(1)
	if e.Source() != "bot-inbox" || e.Author() != "A" || e.Notes() != "a note" {
		t.Errorf("metadata: source=%q author=%q notes=%q", e.Source(), e.Author(), e.Notes())
	}
	if e.SupersedesID() == nil || *e.SupersedesID() != 42 {
		t.Errorf("supersedesID = %v, want 42", e.SupersedesID())
	}
	if got := e.RelatedIDs(); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("relatedIDs = %v, want [2 3] (mixed int/string)", got)
	}
	if e.DateAdded() == nil || e.DateAdded().Format("2006-01-02") != "2026-04-24" {
		t.Errorf("dateAdded = %v, want 2026-04-24", e.DateAdded())
	}
	if e.DateCreated() == nil || e.DateCreated().Format("2006-01-02") != "2026-03-24" {
		t.Errorf("dateCreated = %v, want 2026-03-24", e.DateCreated())
	}
}

func TestDecode_invalidDate(t *testing.T) {
	src := `{"entries":[{"id":1,"habr_id":1,"title":"T","url":"https://h/",` +
		`"category":"golang","status":"keep","date_added":"not-a-date"}]}`
	if _, err := catalogjson.Decode(strings.NewReader(src)); err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestDecode_creation(t *testing.T) {
	src := `{"entries":[{"id":2,"title":"My research",` +
		`"category":"creations","status":"published","lifecycle":"active"}]}`
	c, err := catalogjson.Decode(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	e, ok := c.Find(2)
	if !ok {
		t.Fatal("entry 2 not found")
	}
	if e.Kind() != "creation" {
		t.Errorf("kind = %q, want creation", e.Kind())
	}
	if e.PublishStage() == nil || e.PublishStage().String() != "published" {
		t.Errorf("publish stage = %v, want published", e.PublishStage())
	}
}

func TestDecode_defaultsLifecycleToActive(t *testing.T) {
	src := `{"entries":[{"id":1,"habr_id":1,"title":"T","url":"https://h/",` +
		`"category":"golang","status":"read"}]}`
	c, err := catalogjson.Decode(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	e, _ := c.Find(1)
	if e.Lifecycle().String() != "active" {
		t.Errorf("lifecycle = %q, want active (default)", e.Lifecycle().String())
	}
}

func TestDecode_errors(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"unknown status", articleJSON("bogus")},
		{"invalid json", `{"entries":[`},
		{"invalid category", `{"entries":[{"id":1,"habr_id":1,"title":"T",` +
			`"url":"https://h/","category":"Bad Category","status":"read"}]}`},
		{"invalid lifecycle", `{"entries":[{"id":1,"habr_id":1,"title":"T",` +
			`"url":"https://h/","category":"golang","status":"read","lifecycle":"bogus"}]}`},
		{"duplicate id", `{"entries":[` +
			`{"id":1,"habr_id":1,"title":"A","url":"https://h/","category":"golang","status":"read"},` +
			`{"id":1,"habr_id":2,"title":"B","url":"https://h/","category":"golang","status":"read"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := catalogjson.Decode(strings.NewReader(tt.src)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestLoad_fromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(articleJSON("keep")), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := catalogjson.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
}

func TestLoad_openError(t *testing.T) {
	if _, err := catalogjson.Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error opening missing file, got nil")
	}
}

func TestDecode_unknownStatusIsTyped(t *testing.T) {
	_, err := catalogjson.Decode(strings.NewReader(articleJSON("bogus")))
	if !errors.Is(err, catalogjson.ErrUnknownStatus) {
		t.Fatalf("err = %v, want ErrUnknownStatus", err)
	}
}

func TestDecode_duplicateIDIsDomainError(t *testing.T) {
	src := `{"entries":[` +
		`{"id":1,"habr_id":1,"title":"A","url":"https://h/","category":"golang","status":"read"},` +
		`{"id":1,"habr_id":2,"title":"B","url":"https://h/","category":"golang","status":"read"}]}`
	_, err := catalogjson.Decode(strings.NewReader(src))
	if !errors.Is(err, domain.ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
}

// Словарь категорий лежит в meta.categories: ключ и человеческое название с
// описанием через двоеточие. Читаем строку целиком — где её обрезать, решает
// показ, а описание пригодится подсказкой.
func TestDecode_categoryLabels(t *testing.T) {
	src := `{"meta":{"categories":{"golang":"Go: язык и экосистема"}},` +
		`"entries":[{"id":1,"title":"T","category":"golang","status":"keep"}]}`
	c, err := catalogjson.Decode(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := c.CategoryLabels()["golang"]; got != "Go: язык и экосистема" {
		t.Errorf("label = %q, want %q", got, "Go: язык и экосистема")
	}
}

// Подписи тегов лежат рядом с подписями категорий, в meta.tag_labels. Они
// появились, когда русские теги свели к латинским ключам: ключ уехал в записи,
// читаемое название осталось в словаре.
func TestDecode_tagLabels(t *testing.T) {
	src := `{"meta":{"tag_labels":{"job-market":"Рынок труда"}},` +
		`"entries":[{"id":1,"title":"T","category":"golang","status":"keep","tags":["job-market"]}]}`
	c, err := catalogjson.Decode(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := c.TagLabels()["job-market"]; got != "Рынок труда" {
		t.Errorf("label = %q, want %q", got, "Рынок труда")
	}
}

// Конспект записи лежит в поле file — путь к markdown-файлу разбора. Карточка
// «Здоровье базы» считает, у скольких записей он есть, поэтому поле обязано
// доехать до домена: в движке его не было вовсе.
func TestDecode_notesFile(t *testing.T) {
	src := `{"entries":[{"id":1,"title":"T","category":"golang","status":"keep",` +
		`"file":"notes/2026-05-01_разбор.md"}]}`
	c, err := catalogjson.Decode(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	e, _ := c.Find(1)
	if got := e.NotesFile(); got != "notes/2026-05-01_разбор.md" {
		t.Errorf("NotesFile = %q, want the path from file", got)
	}
}

// Загрузчик — антикоррупционный слой: он обязан принимать всё, что уже лежит
// в файле, включая написания, которые каталог перерастёт после миграции.
// Иначе откат данных или файл со старого бэкапа перестанут читаться.
func TestDecode_considerAliases(t *testing.T) {
	for _, raw := range []string{"consider", "napodumat", "на подумать"} {
		t.Run(raw, func(t *testing.T) {
			src := `{"entries":[{"id":1,"title":"T","category":"golang","status":"` + raw + `"}]}`
			c, err := catalogjson.Decode(strings.NewReader(src))
			if err != nil {
				t.Fatalf("Decode(%q): %v", raw, err)
			}
			e, _ := c.Find(1)
			if e.Verdict() == nil || e.Verdict().String() != "consider" {
				t.Errorf("verdict = %v, want consider", e.Verdict())
			}
		})
	}
}
