package markets

import (
	"cmp"
	"slices"
	"time"
)

// SearchPlace — место товара в выдаче Ozon по одному запросу за период (#670):
// частота запроса, среднее место, последнее место и его день, сколько дней из
// периода товар был в выдаче.
type SearchPlace struct {
	Query      string
	Count, Avg int
	Last       int       // 0 — ни разу не был в выдаче
	LastDay    time.Time // день последнего места; нулевой, если Last = 0
	Seen, Days int
}

// SearchPlaces turns the card's own queries into places: частые первыми, при
// равной частоте — где место выше; limit 0 — все. from — первый день Days.
func SearchPlaces(own []OwnQuery, from time.Time, limit int) []SearchPlace {
	out := make([]SearchPlace, 0, len(own))
	for _, q := range own {
		p := SearchPlace{Query: q.Text, Count: q.Count, Avg: q.Position, Days: len(q.Days)}
		for i, d := range q.Days {
			if d > 0 {
				p.Seen++
				p.Last, p.LastDay = d, from.AddDate(0, 0, i)
			}
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b SearchPlace) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Avg, b.Avg))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// SearchStats — видимость товара в поиске за период и за такой же период до
// него: запросов, по которым товар в выдаче, и среднее место (0 — нет данных).
type SearchStats struct{ Words, WordsBefore, Avg, AvgBefore int }
