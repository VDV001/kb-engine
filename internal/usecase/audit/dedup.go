package audit

import (
	"regexp"
	"sort"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// minNormalizedTitleLen ignores very short normalized titles when grouping by
// title, to avoid grouping unrelated stubs.
const minNormalizedTitleLen = 15

// DuplicateGroup is a set of entries that look like duplicates of each other,
// either by exact URL or by normalized title.
type DuplicateGroup struct {
	Kind     string // "exact-url" | "similar-title"
	Key      string
	EntryIDs []int
}

// Duplicates returns groups of likely-duplicate entries.
func (s *Service) Duplicates() ([]DuplicateGroup, error) {
	c, err := s.loader.Load()
	if err != nil {
		return nil, err
	}
	return duplicates(c), nil
}

var (
	reTranslatePrefix = regexp.MustCompile(`(?i)^\[перевод\]\s*`)
	reTranslateSuffix = regexp.MustCompile(`(?i)\s*\(перевод\)\s*$`)
	// Номер части — часть названия, а не шум в конце: «Часть 1» и «Часть 2»
	// разные материалы. Раньше он срезался, и единственная находка дедупа на
	// живом каталоге оказалась ложной парой из двух половин одной статьи.
	rePartSuffix    = regexp.MustCompile(`(?i)\s*(часть|ч\.)\s*\d+\s*$`)
	reVersionSuffix = regexp.MustCompile(`\s*v?\d+(\.\d+)*\s*$`)
)

// normalizeTitle strips translation markers and a trailing version marker, so
// that two editions of one document ("… v1" / "… v2") collapse to one key.
//
// A part number does NOT collapse. Two rules used to erase it — the part suffix
// itself and the version suffix, which happily eats any trailing digit — so
// removing one of them changed nothing: the pair stayed. The version rule is
// therefore applied only when the tail is not a part number.
func normalizeTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	t = reTranslatePrefix.ReplaceAllString(t, "")
	t = reTranslateSuffix.ReplaceAllString(t, "")
	if !rePartSuffix.MatchString(t) {
		t = reVersionSuffix.ReplaceAllString(t, "")
	}
	return strings.TrimSpace(t)
}

func duplicates(c *domain.Catalog) []DuplicateGroup {
	// A superseded entry is a duplicate someone has already dealt with: it stays
	// in the catalog as the record of an entry filed twice, and reporting it
	// again would keep a resolved pair in the output forever.
	var entries []domain.Entry
	for _, e := range c.Entries() {
		if !e.Lifecycle().IsSuperseded() {
			entries = append(entries, e)
		}
	}

	groups := groupBy(entries, "exact-url", func(e domain.Entry) string { return e.URL() })
	groups = append(groups, groupBy(entries, "similar-title", func(e domain.Entry) string {
		n := normalizeTitle(e.Title())
		if len([]rune(n)) < minNormalizedTitleLen {
			return ""
		}
		return n
	})...)
	return groups
}

// groupBy buckets entries by key(e) (skipping empty keys) and returns the
// buckets with more than one member, in first-seen order with sorted ids.
func groupBy(entries []domain.Entry, kind string, key func(domain.Entry) string) []DuplicateGroup {
	byKey := make(map[string][]int)
	var order []string
	for _, e := range entries {
		k := key(e)
		if k == "" {
			continue
		}
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], e.ID())
	}

	var groups []DuplicateGroup
	for _, k := range order {
		ids := byKey[k]
		if len(ids) > 1 {
			sort.Ints(ids)
			groups = append(groups, DuplicateGroup{Kind: kind, Key: k, EntryIDs: ids})
		}
	}
	return groups
}
