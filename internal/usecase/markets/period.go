package markets

import "time"

// Выбранные дни «Магазина» (#580): окно загружает кабинет за 90 дней один раз
// и режет загруженное по дням, без новых запросов к Ozon. Оба края включительно.

// dayKey — календарный день как число ГГГГММДД: сравнивать дни, а не мгновения.
func dayKey(t time.Time) int { return t.Year()*10000 + int(t.Month())*100 + t.Day() }

// inDays keeps the items whose day falls in from..to; ok=false — дня нет.
func inDays[T any](xs []T, from, to time.Time, day func(T) (time.Time, bool)) []T {
	lo, hi := dayKey(from), dayKey(to)
	var out []T
	for _, x := range xs {
		if d, ok := day(x); ok && dayKey(d) >= lo && dayKey(d) <= hi {
			out = append(out, x)
		}
	}
	return out
}

// PostingsIn keeps postings made on days from..to in from's zone; без даты —
// не входит ни в какой день.
func PostingsIn(posts []OzonPosting, from, to time.Time) []OzonPosting {
	return inDays(posts, from, to, func(p OzonPosting) (time.Time, bool) { return p.At.In(from.Location()), !p.At.IsZero() })
}

// AccrualsIn keeps accruals of days from..to: день начисления — дата (полночь
// UTC), поэтому сравнивается его дата, а не мгновение в зоне окна.
func AccrualsIn(acc []OzonAccrual, from, to time.Time) []OzonAccrual {
	return inDays(acc, from, to, func(a OzonAccrual) (time.Time, bool) { return a.Day.UTC(), true })
}
