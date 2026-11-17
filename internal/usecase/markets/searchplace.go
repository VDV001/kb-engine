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

// SearchDays — период мест по дням: вчера и 30 дней до него, как отдает
// MPStats по умолчанию (31 значение positions, замер 17.11).
const SearchDays = 31

// SearchPlaces turns the card's own queries into places: частые первыми, при
// равной частоте — где место выше, без места — последними; limit 0 — все.
// from — первый день Days; дни не по длине периода — место без даты (ревью #673).
func SearchPlaces(own []OwnQuery, from time.Time, limit int) []SearchPlace {
	out := make([]SearchPlace, 0, len(own))
	for _, q := range own {
		p := SearchPlace{Query: q.Text, Count: q.Count, Avg: q.Position, Days: len(q.Days)}
		dated := len(q.Days) == SearchDays
		for i, d := range q.Days {
			if d > 0 {
				p.Seen++
				p.Last = d
				if dated {
					p.LastDay = from.AddDate(0, 0, i)
				}
			}
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b SearchPlace) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(noPlace(a.Avg), noPlace(b.Avg)), cmp.Compare(a.Avg, b.Avg))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// noPlace orders «нет данных» (0) after any real place.
func noPlace(avg int) int {
	if avg == 0 {
		return 1
	}
	return 0
}

// SearchStats — видимость товара в поиске за период и за такой же период до
// него: запросов, по которым товар в выдаче, и среднее место (0 — нет данных).
type SearchStats struct{ Words, WordsBefore, Avg, AvgBefore int }
