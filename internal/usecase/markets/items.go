package markets

import (
	"cmp"
	"errors"
	"fmt"
	"iter"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNotItems — файл не выгрузка товаров категории.
var ErrNotItems = errors.New("это не выгрузка товаров категории")

// ErrItemsNotDaily — выгрузку товаров категории принесли туда, где нужны
// продажи по дням: в ней итог за период по товарам.
var ErrItemsNotDaily = errors.New("это выгрузка «Товары категории»: итог за период, а не продажи по дням")

// itemColumns — колонки выгрузки MPStats «Товары категории» после FoldName.
// Совпадение точное: «Sales per comment, pcs» — не продажи.
var itemColumns = map[string]string{
	"nichename":          "niche",
	"seller":             "seller",
	"sales,pcs":          "sales",
	"revenue,₽":          "revenue",
	"salesperdayaverage": "perDay",
}

// itemsRequired — без этих колонок ниши из товаров не собрать.
var itemsRequired = []string{"niche", "seller", "sales", "revenue"}

// maxItemProblems — сколько непрочитанных строк называть поименно.
// ponytail: остальные — одной записью с числом; потолок — человек не видит,
// какие именно; путь — список целиком файлом, когда он понадобится.
const maxItemProblems = 100

// daysPerMonth — средняя длина месяца: величины ниш — в месяц.
const daysPerMonth = 365.25 / 12

// nicheSum — товары одной ниши, сложенные по ходу чтения.
type nicheSum struct {
	row            int // первая строка ниши в файле
	sales, revenue float64
	bySeller       map[string]float64 // выручка продавцов, у которых были продажи
}

// itemsRead — состояние чтения: суммы ниш и все, что окно скажет о файле.
type itemsRead struct {
	tb                 Table
	cols               map[string]int
	sums               map[string]*nicheSum
	steady, all        [2]float64 // продажи и продажи в день: у товаров с штукой в день и у всех
	noNiche, noNicheAt int
	hidden             int // непрочитанные строки сверх maxItemProblems
}

// ReadItems builds niches from MPStats «Товары категории» (#580): товар на
// строку, 650-800 МБ, поэтому строки идут потоком. Период в файле не записан:
// он выходит из «Sales / Sales per day average» (см. periodDays). Продажи и выручка — в месяц,
// продавцы — те, у кого были продажи, цена — выручка на штуку, доля топ-10 —
// по выручке продавцов. Рост к прошлому году одна выгрузка не знает. Первая
// строка — заголовок; не тот — ErrNotItems.
func ReadItems(rows iter.Seq2[[]string, error]) (Table, error) {
	rd := itemsRead{tb: Table{HeaderRow: 1}, sums: map[string]*nicheSum{}}
	n := 0
	for r, err := range rows {
		n++
		if err != nil {
			return Table{}, fmt.Errorf("строка %d: %w", n, err)
		}
		if rd.cols == nil {
			if rd.cols = itemsHeader(r); rd.cols == nil {
				return Table{}, ErrNotItems
			}
			continue
		}
		rd.add(n, r)
	}
	if rd.cols == nil {
		return Table{}, ErrNotItems
	}
	return rd.table(), nil
}

// IsItems reports whether a header row is MPStats «Товары категории».
func IsItems(header []string) bool { return itemsHeader(header) != nil }

// itemsHeader finds the columns; nil — это не выгрузка товаров.
func itemsHeader(row []string) map[string]int {
	cols := map[string]int{}
	for i, h := range row {
		if f, ok := itemColumns[domain.FoldName(h)]; ok {
			if _, seen := cols[f]; !seen {
				cols[f] = i
			}
		}
	}
	for _, f := range itemsRequired {
		if _, ok := cols[f]; !ok {
			return nil
		}
	}
	return cols
}

// add reads one product into its niche's sums.
func (rd *itemsRead) add(n int, r []string) {
	if blank(r) {
		return
	}
	niche := strings.TrimSpace(cell(r, rd.cols["niche"]))
	if niche == "" || niche == "0" { // MPStats не отнес товар к нише
		if rd.noNiche == 0 {
			rd.noNicheAt = n
		}
		rd.noNiche++
		return
	}
	v, err := rd.numbers(r)
	if err != nil {
		rd.problem(n, err.Error())
		return
	}
	sales, revenue, perDay := v[0], v[1], v[2]
	s := rd.sums[niche]
	if s == nil {
		s = &nicheSum{row: n, bySeller: map[string]float64{}}
		rd.sums[niche] = s
	}
	rd.tb.Items++
	s.sales += sales
	s.revenue += revenue
	if sales > 0 {
		s.bySeller[strings.TrimSpace(cell(r, rd.cols["seller"]))] += revenue
		if perDay > 0 {
			rd.all[0], rd.all[1] = rd.all[0]+sales, rd.all[1]+perDay
		}
		if perDay >= 1 {
			rd.steady[0], rd.steady[1] = rd.steady[0]+sales, rd.steady[1]+perDay
		}
	}
}

// numbers reads sales, revenue and sales per day; нет колонки — 0.
func (rd *itemsRead) numbers(r []string) ([3]float64, error) {
	var v [3]float64
	for i, f := range []string{"sales", "revenue", "perDay"} {
		at, ok := rd.cols[f]
		if !ok {
			continue
		}
		x, _, _, err := parseNumber(cell(r, at))
		if err != nil || x < 0 {
			return v, fmt.Errorf("«%s»: %w «%s»", f, ErrNotANumber, strings.TrimSpace(cell(r, at)))
		}
		v[i] = x
	}
	return v, nil
}

func (rd *itemsRead) problem(n int, reason string) {
	if len(rd.tb.Problems) >= maxItemProblems {
		rd.hidden++
		return
	}
	rd.tb.Problems = append(rd.tb.Problems, Problem{Row: n, Reason: reason})
}

// table turns the sums into niches, the richest first.
func (rd *itemsRead) table() Table {
	tb := rd.tb
	tb.PeriodDays = rd.periodDays()
	names := slices.SortedFunc(maps.Keys(rd.sums), func(a, b string) int {
		return cmp.Or(cmp.Compare(rd.sums[b].revenue, rd.sums[a].revenue), strings.Compare(a, b))
	})
	for _, name := range names {
		s := rd.sums[name]
		ni, err := domain.NewNiche(name, s.values(tb.PeriodDays))
		if err != nil {
			tb.Problems = append(tb.Problems, Problem{Row: s.row, Reason: err.Error()})
			continue
		}
		tb.Niches = append(tb.Niches, ni)
	}
	if rd.hidden > 0 {
		last := rd.tb.Problems[len(rd.tb.Problems)-1].Row
		tb.Problems = append(tb.Problems, Problem{Row: last, Reason: fmt.Sprintf("и еще %d строк не прочитано", rd.hidden)})
	}
	if rd.noNiche > 0 {
		tb.Problems = append(tb.Problems, Problem{Row: rd.noNicheAt, Reason: fmt.Sprintf("%d товаров без ниши (у MPStats «0») — не учтены", rd.noNiche)})
	}
	slices.SortStableFunc(tb.Problems, func(a, b Problem) int { return a.Row - b.Row })
	return tb
}

// values are the niche's numbers; days 0 — период неизвестен, и продаж и
// выручки в месяц у ниши нет, а не нули.
func (s *nicheSum) values(days int) map[domain.NicheColumn]float64 {
	v := map[domain.NicheColumn]float64{domain.NicheSellers: float64(len(s.bySeller))}
	if days > 0 {
		v[domain.NicheSales] = s.sales / float64(days) * daysPerMonth
		v[domain.NicheRevenue] = s.revenue / float64(days) * daysPerMonth
	}
	if s.sales > 0 {
		v[domain.NichePrice] = s.revenue / s.sales
	}
	if s.revenue > 0 {
		top := slices.Sorted(maps.Values(s.bySeller))
		slices.Reverse(top)
		var sum float64
		for _, x := range top[:min(10, len(top))] {
			sum += x
		}
		v[domain.NicheTop10] = min(1, sum/s.revenue)
	}
	return v
}

// periodDays — длина выгрузки: сумма продаж на сумму продаж в день.
// «Sales per day average» округлен до сотых, и у товара с одной продажей за
// 92 дня деление дает 100: таких большинство, мода по строкам на настоящих
// выгрузках ошибалась на 3-8 дней. Товары, что продают штуку в день и
// больше, дают точное число (92,0 и 153,0 на двух выгрузках #580); нет
// таких — все товары. 0 — продаж нет, период не узнать.
func (rd *itemsRead) periodDays() int {
	for _, v := range [][2]float64{rd.steady, rd.all} {
		if v[1] > 0 {
			return int(math.Round(v[0] / v[1]))
		}
	}
	return 0
}
