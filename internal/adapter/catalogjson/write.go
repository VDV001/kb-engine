package catalogjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
}

// AppendEntries appends entries to the catalog file at path, preserving the
// existing meta and entries (re-indented uniformly, as the legacy Python tool
// also did) and writing atomically. It rejects a file with unexpected top-level
// keys rather than risk dropping data.
func AppendEntries(path string, entries []domain.Entry) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("parse catalog: %w", err)
	}
	for k := range top {
		if k != "meta" && k != "entries" {
			return fmt.Errorf("unsupported top-level key %q: refusing to rewrite catalog", k)
		}
	}

	var existing []json.RawMessage
	if rawEntries, ok := top["entries"]; ok {
		if err := json.Unmarshal(rawEntries, &existing); err != nil {
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

	doc, err := assemble(top["meta"], existing)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, doc)
}

// assemble builds the catalog document compactly (preserving meta and entry key
// order, no HTML escaping) and then indents it uniformly with two spaces.
func assemble(meta json.RawMessage, entries []json.RawMessage) ([]byte, error) {
	var compact bytes.Buffer
	compact.WriteString(`{`)
	if len(meta) > 0 {
		compact.WriteString(`"meta":`)
		if err := json.Compact(&compact, meta); err != nil {
			return nil, fmt.Errorf("compact meta: %w", err)
		}
		compact.WriteByte(',')
	}
	compact.WriteString(`"entries":[`)
	for i, e := range entries {
		if i > 0 {
			compact.WriteByte(',')
		}
		if err := json.Compact(&compact, e); err != nil {
			return nil, fmt.Errorf("compact entry #%d: %w", i, err)
		}
	}
	compact.WriteString("]}")

	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, fmt.Errorf("indent catalog: %w", err)
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
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
	}
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

// writeFileAtomic writes data to a temp file in the same directory and renames
// it over path, so a crash never leaves a half-written catalog.
func writeFileAtomic(path string, data []byte) error {
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
