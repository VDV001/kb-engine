package markets

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Ошибки отчета магазина.
var (
	ErrNotOzonReport = errors.New("это не отчет Ozon «По товарам»")
	ErrShopReport    = errors.New("это отчет магазина по товарам, а не выгрузка ниш: откройте его во вкладке «Магазин»")
)

// OzonReport — отчет Ozon «Аналитика → По товарам» (#580, «Мой магазин»):
// итоги по каждому товару за период. Hints — пояснения Ozon к колонкам
// из строки под заголовком: их окно показывает в подсказках.
type OzonReport struct {
	Period     string
	PeriodDays int
	Total      *domain.ShopProduct
	Products   []*domain.ShopProduct
	Problems   []Problem
	Hints      map[string]string
}

// ozonColumns — колонки отчета по началу заголовка после FoldName. Колонки
// «Динамика» и воронка, которых нет в списке, окно не читает.
var ozonColumns = []struct{ prefix, field string }{
	{"товары", "name"},
	{"sku", "sku"},
	{"артикул", "article"},
	{"схемаработы", "scheme"},
	{"abcанализпосумме", "abc"},
	{"заказанонасумму(поценереализации)", "orderedSum"},
	{"заказанотоваров", "ordered"},
	{"доставленотоваров", "delivered"},
	{"выкупленотоваров", "boughtOut"},
	{"отмененотоваров", "cancelled"},
	{"возвращенотоваров", "returned"},
	{"средняяцена", "avgPrice"},
	{"индексцен", "priceIndex"},
	{"днейвакциях", "promoDays"},
	{"дрр", "drr"},
	{"днейбезостатка", "daysOut"},
	{"остатокнаконецпериода", "stock"},
	{"рекомендацияпопоставке", "supply"},
	{"сколькотоваровпоставить", "supplyQty"},
	{"отзывы", "reviews"},
	{"рейтингтовара", "rating"},
}

// matchOzon adds the columns of one row that cols does not have yet.
func matchOzon(row []string, cols map[string]int) {
	for i, c := range row {
		f := domain.FoldName(c)
		for _, oc := range ozonColumns {
			if _, seen := cols[oc.field]; !seen && strings.HasPrefix(f, oc.prefix) {
				cols[oc.field] = i
				break
			}
		}
	}
}

// ozonHeader finds the header row: в ней «Заказано товаров» и «Выкуплено
// товаров». «Товары», «SKU» и «Схема работы» Ozon ставит строкой выше, в
// строку групп, — их колонки берутся оттуда.
func ozonHeader(rows [][]string) (int, map[string]int) {
	for h := 0; h < len(rows) && h < 20; h++ {
		cols := map[string]int{}
		matchOzon(rows[h], cols)
		_, ordered := cols["ordered"]
		_, bought := cols["boughtOut"]
		if ordered && bought {
			if h > 0 {
				matchOzon(rows[h-1], cols)
			}
			return h, cols
		}
	}
	return -1, nil
}

// isShopReport reports whether rows are a shop report, not a niche export.
func isShopReport(rows [][]string) bool { h, _ := ozonHeader(rows); return h >= 0 }

// ReadOzonProducts reads the report; строку пояснений Ozon и строку итога
// узнает сам, непрочитанные товары — в Problems.
func ReadOzonProducts(rows [][]string) (OzonReport, error) {
	h, cols := ozonHeader(rows)
	if h < 0 {
		return OzonReport{}, ErrNotOzonReport
	}
	r := OzonReport{Hints: map[string]string{}}
	r.Period, r.PeriodDays = ozonPeriod(rows[:h])
	for i := h + 1; i < len(rows); i++ {
		row := rows[i]
		if blank(row) {
			continue
		}
		if i == h+1 && strings.TrimSpace(cell(row, cols["name"])) == "" { // пояснения Ozon под заголовком
			for f, at := range cols {
				r.Hints[f] = strings.TrimSpace(cell(row, at))
			}
			continue
		}
		if err := r.add(row, cols); err != nil {
			r.Problems = append(r.Problems, Problem{Row: i + 1, Reason: err.Error()})
		}
	}
	return r, r.mergeSKUs()
}

// mergeSKUs glues the rows of one SKU: Ozon делит товар по категории, бренду
// и схеме (настоящий отчет #580: 48 строк на 12 SKU). Штуки и суммы
// складываются — на настоящем отчете их сумма сходится с итогом Ozon;
// срочность и дни без остатка — худшее, рейтинг — по отзывам, ДРР — по сумме
// заказов. Товар без SKU не склеивается.
//
// ponytail: остаток — наибольший из строк. Сумма строк на настоящем отчете
// дала 1 293 при итоге Ozon 572, наибольший — 725, первая строка — 509: правило
// Ozon по одному файлу не выводится. Потолок — остаток SKU приблизительный;
// путь — сверить с остатком того же кабинета по API (Stocks), когда будет ключ.
func (r *OzonReport) mergeSKUs() error {
	at := map[string]int{}
	var out []domain.ShopProductData
	for _, p := range r.Products {
		d := p.Data()
		i, seen := at[d.SKU]
		if d.SKU == "" || !seen {
			at[d.SKU] = len(out)
			out = append(out, d)
			continue
		}
		out[i] = mergeShop(out[i], d)
	}
	if len(out) == len(r.Products) {
		return nil
	}
	r.Products = r.Products[:0]
	for _, d := range out {
		p, err := domain.NewShopProduct(d)
		if err != nil {
			return err
		}
		r.Products = append(r.Products, p)
	}
	return nil
}

func mergeShop(a, b domain.ShopProductData) domain.ShopProductData {
	weighted := func(x, wx, y, wy float64) float64 {
		if wx+wy == 0 {
			return max(x, y)
		}
		return (x*wx + y*wy) / (wx + wy)
	}
	a.Rating = weighted(a.Rating, a.Reviews, b.Rating, b.Reviews)
	a.DRR = weighted(a.DRR, a.OrderedSum, b.DRR, b.OrderedSum)
	a.Ordered += b.Ordered
	a.Delivered += b.Delivered
	a.BoughtOut += b.BoughtOut
	a.Cancelled += b.Cancelled
	a.Returned += b.Returned
	a.Refused += b.Refused
	a.OrderedSum += b.OrderedSum
	a.BoughtOutSum += b.BoughtOutSum
	a.Stock = max(a.Stock, b.Stock)
	a.SupplyQty += b.SupplyQty
	a.Reviews += b.Reviews
	a.UrgentSupply = a.UrgentSupply || b.UrgentSupply
	a.DaysOut, a.DaysOutOf = max(a.DaysOut, b.DaysOut), max(a.DaysOutOf, b.DaysOutOf)
	a.PromoDays, a.PeriodDays = max(a.PromoDays, b.PromoDays), max(a.PeriodDays, b.PeriodDays)
	if a.Ordered > 0 {
		a.AvgPrice = a.OrderedSum / a.Ordered
	}
	a.Scheme = joinSchemes(a.Scheme, b.Scheme)
	a.ABC = cmp.Or(min(a.ABC, b.ABC), a.ABC, b.ABC) // A лучше B: берем лучшую непустую
	a.Name, a.Article, a.PriceIndex = cmp.Or(a.Name, b.Name), cmp.Or(a.Article, b.Article), cmp.Or(a.PriceIndex, b.PriceIndex)
	return a
}

// joinSchemes unites «FBS, FBO» and «FBS» into «FBO, FBS».
func joinSchemes(a, b string) string {
	var all []string
	for s := range strings.SplitSeq(a+","+b, ",") {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(all, s) {
			all = append(all, s)
		}
	}
	slices.Sort(all)
	return strings.Join(all, ", ")
}

// add reads one product row; «Итого и среднее» — итог отчета.
func (r *OzonReport) add(row []string, cols map[string]int) error {
	d, err := ozonProduct(row, cols)
	if err != nil {
		return err
	}
	p, err := domain.NewShopProduct(d)
	if err != nil {
		return err
	}
	if strings.HasPrefix(domain.FoldName(d.Name), "итого") {
		r.Total = p
	} else {
		r.Products = append(r.Products, p)
	}
	return nil
}

// ozonPeriod reads «Период: 01.08.2026 – 31.10.2026» above the table.
func ozonPeriod(top [][]string) (string, int) {
	for _, row := range top {
		if v, ok := strings.CutPrefix(strings.TrimSpace(cell(row, 0)), "Период:"); ok {
			v = strings.TrimSpace(v)
			from, to, _ := strings.Cut(v, "–")
			a, okA := parseDay(from)
			b, okB := parseDay(to)
			if okA && okB {
				return v, int(b.Sub(a).Hours()/24) + 1
			}
			return v, 0
		}
	}
	return "", 0
}

// daysOf reads «9 из 92»; прочерк — 0 из 0.
func daysOf(s string) (n, of int, err error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "–" || s == "-" || s == "—" {
		return 0, 0, nil
	}
	a, b, ok := strings.Cut(s, " из ")
	if !ok {
		return 0, 0, fmt.Errorf("%w «%s»", ErrNotANumber, s)
	}
	if n, err = strconv.Atoi(strings.TrimSpace(a)); err == nil {
		of, err = strconv.Atoi(strings.TrimSpace(b))
	}
	if err != nil {
		return 0, 0, fmt.Errorf("%w «%s»", ErrNotANumber, s)
	}
	return n, of, nil
}

func ozonProduct(row []string, cols map[string]int) (domain.ShopProductData, error) {
	text := func(f string) string {
		at, ok := cols[f]
		if !ok {
			return ""
		}
		v := strings.TrimSpace(cell(row, at))
		if v == "–" || v == "-" || v == "—" {
			return ""
		}
		return v
	}
	d := domain.ShopProductData{Name: text("name"), SKU: text("sku"), Article: text("article"), Scheme: text("scheme"), ABC: text("abc"), PriceIndex: text("priceIndex"),
		UrgentSupply: strings.Contains(strings.ToLower(text("supply")), "срочно")}
	nums := map[string]*float64{"orderedSum": &d.OrderedSum, "ordered": &d.Ordered, "delivered": &d.Delivered, "boughtOut": &d.BoughtOut,
		"cancelled": &d.Cancelled, "returned": &d.Returned, "avgPrice": &d.AvgPrice, "drr": &d.DRR, "stock": &d.Stock,
		"supplyQty": &d.SupplyQty, "reviews": &d.Reviews, "rating": &d.Rating}
	for f, dst := range nums {
		v, _, _, err := parseNumber(text(f))
		if err != nil {
			return d, fmt.Errorf("«%s»: %w «%s»", f, ErrNotANumber, text(f))
		}
		*dst = v
	}
	var err error
	if d.DaysOut, d.DaysOutOf, err = daysOf(text("daysOut")); err != nil {
		return d, err
	}
	if d.PromoDays, d.PeriodDays, err = daysOf(text("promoDays")); err != nil {
		return d, err
	}
	return d, nil
}
