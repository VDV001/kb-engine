package markets

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrOzonPeriod — период запроса к Ozon задом наперед.
var ErrOzonPeriod = errors.New("конец периода раньше начала")

// OzonItem — товар в отправлении Ozon.
type OzonItem struct {
	SKU, Article, Name string
	Qty                int
	Price              float64
}

// OzonPosting — отправление Ozon: схема FBO или FBS, статус и причина отмены.
type OzonPosting struct {
	Scheme, Status, CancelReason string
	At                           time.Time // когда отправление ушло в обработку
	Items                        []OzonItem
}

// OzonStock — остаток товара на складах, штук к продаже.
type OzonStock struct {
	SKU, Article, Name string
	Qty                float64
}

// refusedAtPickup — отказ при вручении в причине отмены Ozon.
const refusedAtPickup = "отказался при вручении"

// ozonAcc — товар, сложенный из отправлений.
type ozonAcc struct {
	d       domain.ShopProductData
	schemes []string
}

// OzonShop builds the shop report from a cabinet read through Seller API
// (#580): товар на SKU — заказано, доставлено, отменено и отказ при
// вручении из отправлений, остаток со складов. Возвратов после покупки в
// отправлениях нет — их окно не считает. Ozon «Срочно поставить» по API не
// отдает: срочно — когда запаса меньше чем на две недели продаж (без отмен).
func OzonShop(posts []OzonPosting, stock []OzonStock, from, to time.Time) (OzonReport, error) {
	if to.Before(from) {
		return OzonReport{}, ErrOzonPeriod
	}
	days := calendarDays(from, to)
	acc := map[string]*ozonAcc{}
	get := func(sku, article string) *ozonAcc {
		a := acc[sku]
		if a == nil {
			a = &ozonAcc{d: domain.ShopProductData{SKU: sku, Article: article, PeriodDays: days}}
			acc[sku] = a
		}
		return a
	}
	for _, p := range posts {
		for _, it := range p.Items {
			a := get(it.SKU, it.Article)
			a.add(p, it)
		}
	}
	for _, s := range stock {
		a := get(s.SKU, s.Article)
		a.d.Stock += max(s.Qty, 0)
		if a.d.Name == "" {
			a.d.Name = s.Name // у товара без заказов название — из карточки
		}
	}
	r := OzonReport{Period: from.Format("02.01.2006") + " – " + to.Format("02.01.2006"), PeriodDays: days, Hints: map[string]string{}}
	total := domain.ShopProductData{Name: "Итого и среднее", PeriodDays: days}
	for _, a := range acc {
		d := a.finish(days)
		p, err := domain.NewShopProduct(d)
		if err != nil {
			r.Problems = append(r.Problems, Problem{Reason: fmt.Sprintf("SKU %s: %v", d.SKU, err)})
			continue
		}
		r.Products = append(r.Products, p)
		total.Ordered += d.Ordered
		total.Delivered += d.Delivered
		total.BoughtOut += d.BoughtOut
		total.Cancelled += d.Cancelled
		total.Refused += d.Refused
		total.OrderedSum += d.OrderedSum
		total.BoughtOutSum += d.BoughtOutSum
		total.Stock += d.Stock
	}
	slices.SortFunc(r.Products, func(a, b *domain.ShopProduct) int {
		return cmp.Or(cmp.Compare(b.Data().Ordered, a.Data().Ordered), cmp.Compare(a.Data().SKU, b.Data().SKU))
	})
	if total.Ordered > 0 {
		total.AvgPrice = total.OrderedSum / total.Ordered
	}
	var err error
	if r.Total, err = domain.NewShopProduct(total); err != nil {
		return OzonReport{}, err
	}
	return r, nil
}

// add counts one item of a posting.
func (a *ozonAcc) add(p OzonPosting, it OzonItem) {
	q := float64(it.Qty)
	if a.d.Name == "" {
		a.d.Name = it.Name
	}
	if !slices.Contains(a.schemes, p.Scheme) && p.Scheme != "" {
		a.schemes = append(a.schemes, p.Scheme)
	}
	a.d.Ordered += q
	a.d.OrderedSum += q * it.Price
	switch p.Status {
	case "cancelled":
		a.d.Cancelled += q
		if strings.Contains(strings.ToLower(p.CancelReason), refusedAtPickup) {
			a.d.Refused += q
		}
	case "delivered":
		a.d.Delivered += q
		a.d.BoughtOut += q
		a.d.BoughtOutSum += q * it.Price
	}
}

// finish fills the price, the scheme and the supply advice.
func (a *ozonAcc) finish(days int) domain.ShopProductData {
	d := a.d
	if d.Name == "" {
		d.Name = d.Article
	}
	slices.Sort(a.schemes)
	d.Scheme = strings.Join(a.schemes, ", ")
	if d.Ordered > 0 {
		d.AvgPrice = d.OrderedSum / d.Ordered
	}
	d.UrgentSupply, d.SupplyQty = domain.Supply(d.Ordered-d.Cancelled, d.Stock, days)
	return d
}

// calendarDays counts days from from to to inclusive by the calendar: разность
// часов на переходе на летнее время дала бы на день меньше (ревью #590).
func calendarDays(from, to time.Time) int {
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	return int(day(to).Sub(day(from)).Hours()/24) + 1
}

// OzonDaily turns postings into own sales by day for the forecast (#580):
// штуки без отмен по дню отправления в часовом поясе loc, дни from..to
// включительно; день без продаж — ноль, а не пропуск.
func OzonDaily(posts []OzonPosting, from, to time.Time, loc *time.Location) (domain.DailySales, error) {
	key := func(t time.Time) time.Time {
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	first, last := key(from), key(to)
	if last.Before(first) {
		return domain.DailySales{}, ErrOzonPeriod
	}
	byDay := map[time.Time]float64{}
	for _, p := range posts {
		d := key(p.At)
		if p.Status == "cancelled" || p.At.IsZero() || d.Before(first) || d.After(last) {
			continue
		}
		for _, it := range p.Items {
			byDay[d] += float64(it.Qty)
		}
	}
	var days []domain.DaySales
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		days = append(days, domain.DaySales{Day: d, Value: byDay[d]})
	}
	return domain.NewDailySales(days)
}
