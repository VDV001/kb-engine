package markets

import "time"

// Выбранные дни «Магазина» (#580): окно загружает кабинет за 90 дней один раз
// и режет загруженное по дням, без новых запросов к Ozon. Оба края включительно.

// dayKey — календарный день как число ГГГГММДД: сравнивать дни, а не мгновения.
func dayKey(t time.Time) int { return t.Year()*10000 + int(t.Month())*100 + t.Day() }

// PostingsIn keeps postings made on days from..to in from's zone; без даты —
// не входит ни в какой день.
func PostingsIn(posts []OzonPosting, from, to time.Time) []OzonPosting {
	lo, hi := dayKey(from), dayKey(to)
	var out []OzonPosting
	for _, p := range posts {
		if p.At.IsZero() {
			continue
		}
		if d := dayKey(p.At.In(from.Location())); d >= lo && d <= hi {
			out = append(out, p)
		}
	}
	return out
}

// AccrualsIn keeps accruals of days from..to: день начисления — дата (полночь
// UTC), поэтому сравнивается его дата, а не мгновение в зоне окна.
func AccrualsIn(acc []OzonAccrual, from, to time.Time) []OzonAccrual {
	lo, hi := dayKey(from), dayKey(to)
	var out []OzonAccrual
	for _, a := range acc {
		if d := dayKey(a.Day.UTC()); d >= lo && d <= hi {
			out = append(out, a)
		}
	}
	return out
}

// ReturnsIn keeps returns whose status changed on days from..to in from's zone.
func ReturnsIn(rets []OzonReturn, from, to time.Time) []OzonReturn {
	lo, hi := dayKey(from), dayKey(to)
	var out []OzonReturn
	for _, r := range rets {
		if d := dayKey(r.Changed.In(from.Location())); d >= lo && d <= hi {
			out = append(out, r)
		}
	}
	return out
}
