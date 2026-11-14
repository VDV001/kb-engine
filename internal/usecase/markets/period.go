package markets

import (
	"cmp"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

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

// NicheSale — ниша категории из сервиса аналитики за период.
type NicheSale struct {
	Name           string
	Sales, Revenue float64             // за весь период
	Sellers        int                 // с продажами
	Top10Revenue   float64             // выручка десяти крупнейших продавцов; 0 — не знаю
	Months         []domain.MonthSales // продажи по месяцам для сезона; пусто — не знаю
}

// MPStatsNiches builds niches of category parent from MPStats (#580): продажи
// и выручка — в месяц, цена — выручка на штуку, рост — к тем же дням год
// назад (prev), если тогда были продажи; имя — полный путь категории, по нему
// «Прогноз» берет нишу из MPStats. Богатые сначала.
func MPStatsNiches(parent string, cur, prev []NicheSale, days int) (Table, error) {
	if days <= 0 {
		return Table{}, ErrOzonPeriod
	}
	before := map[string]float64{}
	for _, p := range prev {
		before[p.Name] = p.Sales
	}
	sorted := slices.Clone(cur)
	slices.SortStableFunc(sorted, func(a, b NicheSale) int { return cmp.Compare(b.Revenue, a.Revenue) })
	tb := Table{PeriodDays: days}
	for _, n := range sorted {
		v := map[domain.NicheColumn]float64{
			domain.NicheSales:   n.Sales / float64(days) * daysPerMonth,
			domain.NicheRevenue: n.Revenue / float64(days) * daysPerMonth,
			domain.NicheSellers: float64(n.Sellers),
		}
		if n.Sales > 0 {
			v[domain.NichePrice] = n.Revenue / n.Sales
		}
		if b := before[n.Name]; b > 0 {
			v[domain.NicheGrowth] = n.Sales/b - 1
		}
		if n.Top10Revenue > 0 && n.Revenue > 0 {
			v[domain.NicheTop10] = min(1, n.Top10Revenue/n.Revenue)
		}
		name := n.Name // без parent — верхняя категория (весь Ozon, отзыв 14.11)
		if parent != "" {
			name = parent + "/" + n.Name
		}
		ni, err := domain.NewNiche(name, v)
		if err == nil && len(n.Months) > 0 {
			ni, err = ni.WithMonths(n.Months)
		}
		if err != nil {
			tb.Problems = append(tb.Problems, Problem{Reason: n.Name + ": " + err.Error()})
			continue
		}
		tb.Niches = append(tb.Niches, ni)
	}
	return tb, nil
}
