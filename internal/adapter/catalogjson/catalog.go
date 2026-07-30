// Package catalogjson loads a KB catalog from its on-disk JSON format into the
// domain model. It is the anti-corruption layer: all messy legacy encodings
// (mixed-case verdicts, the single status field that conflated verdict /
// read-state / publish-stage, missing lifecycle) are normalized here so the
// domain stays strict.
package catalogjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrUnknownStatus is returned when a stored status value maps to no known
// verdict, read-state or publish-stage.
var ErrUnknownStatus = errors.New("unknown status")

// flexInt decodes a JSON field that the catalog stores inconsistently as a
// number, a numeric string, or null/absent (e.g. habr_id).
type flexInt struct {
	value int
	set   bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if n, err := strconv.Atoi(string(b)); err == nil { // bare JSON number
		f.value, f.set = n, true
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("flexInt: %w", err)
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("%q is not an integer: %w", s, err)
	}
	f.value, f.set = n, true
	return nil
}

// pointer returns a *int, or nil when the value was absent/null.
func (f flexInt) pointer() *int {
	if !f.set {
		return nil
	}
	v := f.value
	return &v
}

type entryDTO struct {
	ID           int       `json:"id"`
	HabrID       flexInt   `json:"habr_id"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	Category     string    `json:"category"`
	Status       string    `json:"status"`
	Lifecycle    string    `json:"lifecycle"`
	Description  string    `json:"description"`
	Tags         []string  `json:"tags"`
	Source       string    `json:"source"`
	Author       string    `json:"author"`
	Notes        string    `json:"notes"`
	File         string    `json:"file"`
	SupersedesID flexInt   `json:"supersedes_id"`
	RelatedIDs   []flexInt `json:"related_ids"`
	DateAdded    string    `json:"date_added"`
	DateCreated  string    `json:"date_created"`
}

type catalogDTO struct {
	Meta    metaDTO    `json:"meta"`
	Entries []entryDTO `json:"entries"`
}

// metaDTO is the catalog's own header. Only the naming of categories is read:
// the rest of meta is bookkeeping the engine derives for itself.
type metaDTO struct {
	Categories map[string]string `json:"categories"`
}

// verdictAliases maps legacy status spellings to the canonical verdict value.
var verdictAliases = map[string]string{
	"keep":             "keep",
	"KEEP":             "keep",
	"consider":         "consider",
	"napodumat":        "consider",
	"на подумать":      "consider",
	"skip":             "skip",
	"SKIP":             "skip",
	"skip-unavailable": "skip-unavailable",
	"SKIP-unavailable": "skip-unavailable",
}

var readStateValues = map[string]struct{}{"read": {}, "unread": {}}

var publishStageValues = map[string]struct{}{"draft": {}, "final": {}, "published": {}}

// triage is the decoded meaning of a legacy status value.
type triage struct {
	kind         string
	verdict      *domain.Verdict
	readState    *domain.ReadState
	publishStage *domain.PublishStage
}

// mapStatus splits the single legacy status value into the typed domain aspects.
// A verdict implies the article was read; a bare read/unread is an undecided
// article; a publish stage marks an owner creation.
func mapStatus(raw string) (triage, error) {
	if canon, ok := verdictAliases[raw]; ok {
		v, err := domain.NewVerdict(canon)
		if err != nil {
			return triage{}, err
		}
		rs, err := domain.NewReadState("read")
		if err != nil {
			return triage{}, err
		}
		return triage{kind: domain.KindArticle, verdict: &v, readState: &rs}, nil
	}
	if _, ok := readStateValues[raw]; ok {
		rs, err := domain.NewReadState(raw)
		if err != nil {
			return triage{}, err
		}
		return triage{kind: domain.KindArticle, readState: &rs}, nil
	}
	if _, ok := publishStageValues[raw]; ok {
		ps, err := domain.NewPublishStage(raw)
		if err != nil {
			return triage{}, err
		}
		return triage{kind: domain.KindCreation, publishStage: &ps}, nil
	}
	// A lifecycle value echoed into status (id=1312: status="active" next to
	// lifecycle="active") is storage noise, not triage. The entry was looked at,
	// but no verdict was recorded — decode it as read-without-verdict. The domain
	// constructor is the membership test on purpose: duplicating the lifecycle
	// list here would let the two drift apart.
	if _, err := domain.NewLifecycle(raw); err == nil {
		rs, err := domain.NewReadState("read")
		if err != nil {
			return triage{}, err
		}
		return triage{kind: domain.KindArticle, readState: &rs}, nil
	}
	return triage{}, fmt.Errorf("%w: %q", ErrUnknownStatus, raw)
}

func toEntry(dto entryDTO) (domain.Entry, error) {
	cat, err := domain.NewCategory(dto.Category)
	if err != nil {
		return domain.Entry{}, err
	}
	lifecycleRaw := dto.Lifecycle
	if strings.TrimSpace(lifecycleRaw) == "" {
		lifecycleRaw = "active"
	}
	lc, err := domain.NewLifecycle(lifecycleRaw)
	if err != nil {
		return domain.Entry{}, err
	}
	tr, err := mapStatus(dto.Status)
	if err != nil {
		return domain.Entry{}, err
	}
	dateAdded, err := parseDate(dto.DateAdded)
	if err != nil {
		return domain.Entry{}, fmt.Errorf("date_added: %w", err)
	}
	dateCreated, err := parseDate(dto.DateCreated)
	if err != nil {
		return domain.Entry{}, fmt.Errorf("date_created: %w", err)
	}
	return domain.NewEntry(domain.EntryParams{
		ID:           dto.ID,
		Kind:         tr.kind,
		Title:        dto.Title,
		Category:     cat,
		Lifecycle:    lc,
		HabrID:       dto.HabrID.pointer(),
		URL:          dto.URL,
		Verdict:      tr.verdict,
		ReadState:    tr.readState,
		PublishStage: tr.publishStage,
		Tags:         dto.Tags,
		Description:  dto.Description,
		Source:       dto.Source,
		Author:       dto.Author,
		Notes:        dto.Notes,
		NotesFile:    dto.File,
		SupersedesID: dto.SupersedesID.pointer(),
		RelatedIDs:   flexIntsToInts(dto.RelatedIDs),
		DateAdded:    dateAdded,
		DateCreated:  dateCreated,
	})
}

// parseDate parses a "YYYY-MM-DD" date (ignoring any time suffix). Empty input
// yields nil; a malformed value is an error.
func parseDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > 10 {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	return &t, nil
}

func flexIntsToInts(fs []flexInt) []int {
	if len(fs) == 0 {
		return nil
	}
	out := make([]int, 0, len(fs))
	for _, f := range fs {
		if f.set {
			out = append(out, f.value)
		}
	}
	return out
}

// Decode reads a catalog JSON document and builds a domain Catalog. Any entry
// that violates an invariant aborts the load with a contextual error.
func Decode(r io.Reader) (*domain.Catalog, error) {
	var dto catalogDTO
	if err := json.NewDecoder(r).Decode(&dto); err != nil {
		return nil, fmt.Errorf("decode catalog json: %w", err)
	}
	entries := make([]domain.Entry, 0, len(dto.Entries))
	for i, ed := range dto.Entries {
		e, err := toEntry(ed)
		if err != nil {
			return nil, fmt.Errorf("entry #%d (id=%d): %w", i, ed.ID, err)
		}
		entries = append(entries, e)
	}
	return domain.NewCatalog(entries, domain.WithCategoryLabels(dto.Meta.Categories))
}

// Load reads and decodes a catalog from the file at path.
func Load(path string) (*domain.Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	defer func() { _ = f.Close() }()
	return Decode(f)
}

// FileLoader loads a catalog from a fixed file path. It satisfies the
// CatalogLoader port expected by use cases.
type FileLoader struct {
	Path string
}

// Load reads and decodes the catalog at the configured path.
func (l FileLoader) Load() (*domain.Catalog, error) {
	return Load(l.Path)
}
