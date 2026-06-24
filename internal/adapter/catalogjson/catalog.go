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
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrUnknownStatus is returned when a stored status value maps to no known
// verdict, read-state or publish-stage.
var ErrUnknownStatus = errors.New("unknown status")

type entryDTO struct {
	ID          int      `json:"id"`
	HabrID      *int     `json:"habr_id"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Category    string   `json:"category"`
	Status      string   `json:"status"`
	Lifecycle   string   `json:"lifecycle"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type catalogDTO struct {
	Entries []entryDTO `json:"entries"`
}

// verdictAliases maps legacy status spellings to the canonical verdict value.
var verdictAliases = map[string]string{
	"keep":             "keep",
	"KEEP":             "keep",
	"napodumat":        "napodumat",
	"на подумать":      "napodumat",
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
		return triage{kind: "article", verdict: &v, readState: &rs}, nil
	}
	if _, ok := readStateValues[raw]; ok {
		rs, err := domain.NewReadState(raw)
		if err != nil {
			return triage{}, err
		}
		return triage{kind: "article", readState: &rs}, nil
	}
	if _, ok := publishStageValues[raw]; ok {
		ps, err := domain.NewPublishStage(raw)
		if err != nil {
			return triage{}, err
		}
		return triage{kind: "creation", publishStage: &ps}, nil
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
	return domain.NewEntry(domain.EntryParams{
		ID:           dto.ID,
		Kind:         tr.kind,
		Title:        dto.Title,
		Category:     cat,
		Lifecycle:    lc,
		HabrID:       dto.HabrID,
		URL:          dto.URL,
		Verdict:      tr.verdict,
		ReadState:    tr.readState,
		PublishStage: tr.publishStage,
		Tags:         dto.Tags,
		Description:  dto.Description,
	})
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
	return domain.NewCatalog(entries)
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
