package catalogjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filebackup"
	"github.com/daniil/kb-engine/internal/domain"
)

// legacyEntry is the on-disk JSON shape written for newly appended entries. It
// recombines the typed domain aspects (verdict / read-state / publish-stage)
// back into the single legacy "status" field, mirroring the split done on read.
type legacyEntry struct {
	ID           int      `json:"id"`
	HabrID       *int     `json:"habr_id,omitempty"`
	Title        string   `json:"title"`
	Type         string   `json:"type"`
	Category     string   `json:"category"`
	Description  string   `json:"description"`
	URL          string   `json:"url"`
	Tags         []string `json:"tags"`
	DateCreated  string   `json:"date_created,omitempty"`
	DateAdded    string   `json:"date_added,omitempty"`
	Status       string   `json:"status"`
	Source       string   `json:"source,omitempty"`
	Author       string   `json:"author,omitempty"`
	Lifecycle    string   `json:"lifecycle"`
	SupersedesID *int     `json:"supersedes_id,omitempty"`
	RelatedIDs   []int    `json:"related_ids,omitempty"`
	Notes        string   `json:"notes"`
	File         string   `json:"file,omitempty"`
	Version      string   `json:"version,omitempty"`
	Revision     *int     `json:"revision,omitempty"`
}

// member is one top-level key with its raw value, kept in file order.
type member struct {
	key string
	val json.RawMessage
}

// AppendEntries appends entries to the catalog file at path, preserving the
// existing entries (re-indented uniformly, as the legacy Python tool also did)
// and writing atomically. Top-level keys the domain knows nothing about — the
// live catalog's "last_updated", written by the Python dashboard — are carried
// through verbatim, in the order the file already had them.
//
// It is intended for freshly created entries (e.g. inbox imports). Existing
// entries already in the file are preserved verbatim as raw JSON, never re-
// encoded — so the lossy projection in toLegacy (which only models the fields
// the domain knows) is never applied to data loaded from disk.
func AppendEntries(path string, entries []domain.Entry) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}

	members, err := readTopLevel(raw)
	if err != nil {
		return err
	}

	var existing []json.RawMessage
	for _, m := range members {
		if m.key != "entries" {
			continue
		}
		if err := json.Unmarshal(m.val, &existing); err != nil {
			return fmt.Errorf("parse entries: %w", err)
		}
	}

	for _, e := range entries {
		encoded, err := marshalNoEscape(toLegacy(e))
		if err != nil {
			return fmt.Errorf("encode entry %d: %w", e.ID(), err)
		}
		existing = append(existing, encoded)
	}

	doc, err := assemble(members, existing)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, doc)
}

// readTopLevel returns the object's members in file order. Order is what keeps a
// rewrite out of the diff on every line but the one that changed, and a map
// cannot give it — Go randomises the iteration deliberately.
func readTopLevel(raw []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("parse catalog: expected an object at the top level, found %v", tok)
	}

	var members []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("parse catalog: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("parse catalog: expected a string key, found %T", tok)
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, fmt.Errorf("parse %q: %w", key, err)
		}
		members = append(members, member{key: key, val: val})
	}
	return members, nil
}

// assemble builds the catalog document compactly (preserving top-level and entry
// key order, no HTML escaping) and then indents it uniformly with two spaces.
// A file that had no "entries" key gets one appended, so the entries never
// silently go nowhere.
func assemble(members []member, entries []json.RawMessage) ([]byte, error) {
	var compact bytes.Buffer
	compact.WriteString(`{`)

	wroteEntries := false
	for _, m := range members {
		if compact.Len() > 1 {
			compact.WriteByte(',')
		}
		if err := writeKey(&compact, m.key); err != nil {
			return nil, err
		}
		if m.key == "entries" {
			if err := writeEntries(&compact, entries); err != nil {
				return nil, err
			}
			wroteEntries = true
			continue
		}
		if err := json.Compact(&compact, m.val); err != nil {
			return nil, fmt.Errorf("compact %q: %w", m.key, err)
		}
	}
	if !wroteEntries {
		if compact.Len() > 1 {
			compact.WriteByte(',')
		}
		if err := writeKey(&compact, "entries"); err != nil {
			return nil, err
		}
		if err := writeEntries(&compact, entries); err != nil {
			return nil, err
		}
	}
	compact.WriteString("}")

	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, fmt.Errorf("indent catalog: %w", err)
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// writeKey writes a quoted object key followed by its colon.
func writeKey(buf *bytes.Buffer, key string) error {
	encoded, err := marshalNoEscape(key)
	if err != nil {
		return fmt.Errorf("encode key %q: %w", key, err)
	}
	buf.Write(encoded)
	buf.WriteByte(':')
	return nil
}

// writeEntries writes the entries array compactly.
func writeEntries(buf *bytes.Buffer, entries []json.RawMessage) error {
	buf.WriteByte('[')
	for i, e := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := json.Compact(buf, e); err != nil {
			return fmt.Errorf("compact entry #%d: %w", i, err)
		}
	}
	buf.WriteByte(']')
	return nil
}

// marshalNoEscape marshals v compactly without escaping <, > and & — so URLs
// with query separators stay readable, matching ensure_ascii=False output.
func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// toLegacy projects a domain Entry onto the legacy on-disk shape.
func toLegacy(e domain.Entry) legacyEntry {
	tags := e.Tags()
	if tags == nil {
		tags = []string{}
	}
	return legacyEntry{
		ID:           e.ID(),
		HabrID:       e.HabrID(),
		Title:        e.Title(),
		Type:         legacyType(e.Kind()),
		Category:     e.Category().String(),
		Description:  e.Description(),
		URL:          e.URL(),
		Tags:         tags,
		DateCreated:  formatDate(e.DateCreated()),
		DateAdded:    formatDate(e.DateAdded()),
		Status:       legacyStatus(e),
		Source:       e.Source(),
		Author:       e.Author(),
		Lifecycle:    e.Lifecycle().String(),
		SupersedesID: e.SupersedesID(),
		RelatedIDs:   e.RelatedIDs(),
		Notes:        e.Notes(),
		File:         e.NotesFile(),
		Version:      versionString(e),
		Revision:     e.Revision(),
	}
}

// versionString renders the semver of an own artefact, or "" when the entry
// carries an edition counter instead — the domain refuses to hold both.
func versionString(e domain.Entry) string {
	if v := e.Version(); v != nil {
		return v.String()
	}
	return ""
}

// legacyType maps a domain kind to the legacy "type" field. Bot-imported
// articles have historically been stored as "link".
func legacyType(kind string) string {
	if kind == domain.KindCreation {
		return "creation"
	}
	return "link"
}

// legacyStatus recombines the typed aspects into one status string: a verdict
// (once decided) wins, otherwise the read-state, otherwise the publish-stage.
func legacyStatus(e domain.Entry) string {
	if v := e.Verdict(); v != nil {
		return v.String()
	}
	if rs := e.ReadState(); rs != nil {
		return rs.String()
	}
	if ps := e.PublishStage(); ps != nil {
		return ps.String()
	}
	return ""
}

func formatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// backupsKept — та же глубина, что у журнала и книги: хватает откатить
// неудачный день и мало настолько, чтобы папка снимков не росла молча.
const backupsKept = 10

// writeFileAtomic writes data to a temp file in the same directory and renames
// it over path, so a crash never leaves a half-written catalog.
//
// Перед заменой снимается копия. Атомарность бережёт от обрывка файла, но не
// от неверного содержимого, записанного целиком и успешно: `set --related`
// заменяет список связей, а не дополняет, а ошибочная миграция проходит по
// всей базе разом. Каталог намеренно не под git — рядом с ним лежат личные
// файлы владельца, — поэтому снимок здесь единственный механизм отката.
//
// Снимок стоит в этой функции, а не в семи вызывающих: через неё проходят все
// писатели каталога, и защита, размноженная по вызовам, однажды не доедет до
// нового.
func writeFileAtomic(path string, data []byte) error {
	if err := filebackup.Snapshot(path, time.Now, backupsKept); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".catalog-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
