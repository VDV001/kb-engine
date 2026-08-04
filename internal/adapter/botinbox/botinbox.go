// Package botinbox is the inbound anti-corruption layer for articles delivered
// by the Habr collector bot. It decodes the bot's JSON and maps each external
// article onto domain.EntryParams (everything except the id, which the ingest
// use case allocates against a catalog). All messy normalization — hub→category
// mapping, tag cleanup, description truncation, date parsing — lives here so the
// domain stays strict.
package botinbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Article is one item as produced by the bot inbox export.
type Article struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Hub         string   `json:"hub"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	CreatedAt   string   `json:"createdAt"`
	Description string   `json:"description"`
}

// hubCategory maps a Habr hub slug to a KB category. Hubs with no entry fall
// back to "dev-practices". Mirrors the historical process_inbox.py table.
var hubCategory = map[string]string{
	"go":                          "golang",
	"typescript":                  "typescript",
	"nodejs":                      "nodejs",
	"nestjs":                      "nodejs",
	"devops":                      "devops",
	"postgresql":                  "databases",
	"reactjs":                     "frontend",
	"artificial_intelligence":     "data-science",
	"machine_learning":            "data-science",
	"natural_language_processing": "data-science",
	"image_processing":            "data-science",
	"bigdata":                     "data-science",
	"cloud_services":              "devops",
}

const (
	fallbackCategory = "dev-practices"
	defaultTitle     = "Untitled"
	maxTags          = 8
	maxDescription   = 200
)

// DecodeArticles reads the bot inbox JSON, accepting either an array of articles
// or a single article object, and returns them as a slice.
func DecodeArticles(r io.Reader) ([]Article, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read inbox: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty inbox payload")
	}
	if trimmed[0] == '[' {
		var arts []Article
		if err := json.Unmarshal(trimmed, &arts); err != nil {
			return nil, fmt.Errorf("decode inbox array: %w", err)
		}
		return arts, nil
	}
	var single Article
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return nil, fmt.Errorf("decode inbox object: %w", err)
	}
	return []Article{single}, nil
}

// MapArticle maps a bot Article onto domain.EntryParams without an id. The
// ingest use case assigns the id (and date-added) before constructing the
// entity. now supplies the creation date when the article carries none.
func MapArticle(a Article, now time.Time) (domain.EntryParams, error) {
	categoryName := hubCategory[a.Hub]
	if categoryName == "" {
		categoryName = fallbackCategory
	}
	cat, err := domain.NewCategory(categoryName)
	if err != nil {
		return domain.EntryParams{}, err
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		return domain.EntryParams{}, err
	}
	unread, err := domain.NewReadState("unread")
	if err != nil {
		return domain.EntryParams{}, err
	}

	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = defaultTitle
	}

	created := parseCreatedDate(a.CreatedAt, now)

	// Номер статьи есть в адресе, и не перенести его — значит потерять то, что
	// уже известно. Чужой источник номера не получает: пустое поле честнее
	// выдуманного, потому что по этому полю потом ищут дубли.
	var habrID *int
	if id := HabrIDFromURL(a.URL); id != 0 {
		habrID = &id
	}

	return domain.EntryParams{
		Kind:        domain.KindArticle,
		HabrID:      habrID,
		Title:       title,
		Category:    cat,
		Lifecycle:   lc,
		URL:         a.URL,
		ReadState:   &unread,
		Tags:        cleanTags(a.Hub, a.Tags),
		Description: truncateRunes(a.Description, maxDescription),
		Source:      "bot-inbox",
		Author:      a.Author,
		Notes:       buildNotes(a.Hub, a.Author),
		DateCreated: &created,
	}, nil
}

// cleanTags prepends the hub to the article tags, deduplicates while preserving
// first-seen order, normalizes (lowercase, spaces and dots to hyphens), drops
// empties, and caps the result at maxTags.
func cleanTags(hub string, tags []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, raw := range append([]string{hub}, tags...) {
		t := normalizeTag(raw)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
		if len(out) == maxTags {
			break
		}
	}
	return out
}

func normalizeTag(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// truncateRunes returns s limited to at most n runes (not bytes).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// parseCreatedDate takes the leading YYYY-MM-DD of an ISO timestamp. A missing
// or unparseable value falls back to now (date only).
func parseCreatedDate(createdAt string, now time.Time) time.Time {
	s := strings.TrimSpace(createdAt)
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t
		}
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func buildNotes(hub, author string) string {
	if author == "" {
		author = "unknown"
	}
	return fmt.Sprintf("Auto-imported from Habr (%s). Author: %s", hub, author)
}
