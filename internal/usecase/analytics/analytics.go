// Package analytics computes the dynamic dashboard metrics from a catalog:
// growth over time and category sizes. The semantic layer (curated patterns,
// gaps, contradictions) is data that lives in analytics_config.json and is
// passed through untouched by callers — only the numbers are computed here.
package analytics

import (
	"sort"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// WeekCount is the number of entries created in one week-long bucket, labelled
// by the bucket's start date (DD.MM).
type WeekCount struct {
	Week  string `json:"week"`
	Count int    `json:"count"`
}

// CategorySize is the number of entries in a category.
type CategorySize struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// GrowthByWeek buckets entries by creation date into the last `weeks` weeks,
// returned oldest-first. Entries without a creation date, or older than the
// window, are ignored. The label is the bucket's start date as DD.MM.
func GrowthByWeek(c *domain.Catalog, now time.Time, weeks int) []WeekCount {
	buckets := make([]int, weeks)
	for _, e := range c.Entries() {
		created := e.DateCreated()
		if created == nil {
			continue
		}
		days := int(now.Sub(*created).Hours() / 24)
		if days < 0 || days >= weeks*7 {
			continue
		}
		buckets[days/7]++
	}

	out := make([]WeekCount, 0, weeks)
	for i := weeks - 1; i >= 0; i-- {
		start := now.AddDate(0, 0, -(i+1)*7)
		out = append(out, WeekCount{Week: start.Format("02.01"), Count: buckets[i]})
	}
	return out
}

// CategorySizes returns per-category entry counts, sorted by count descending
// then category name ascending for stable output.
func CategorySizes(c *domain.Catalog) []CategorySize {
	counts := make(map[string]int)
	for _, e := range c.Entries() {
		counts[e.Category().String()]++
	}
	out := make([]CategorySize, 0, len(counts))
	for cat, n := range counts {
		out = append(out, CategorySize{Category: cat, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Category < out[j].Category
	})
	return out
}
