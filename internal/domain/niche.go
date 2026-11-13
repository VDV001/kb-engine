package domain

import (
	"errors"
	"fmt"
	"maps"
	"strings"
)

// ErrInvalidNiche is returned when a niche breaks its rules: no name, a
// negative count or price, a share outside 0..1, a fall below -100 %.
var ErrInvalidNiche = errors.New("ниша не подходит")

// ErrUnknownNicheColumn is returned for a column id the niche file does not have.
var ErrUnknownNicheColumn = errors.New("такой колонки у файла ниш нет")

// NicheColumn — колонка выгрузки ниш из сервиса аналитики (#580). Файлы у
// сервисов разные, поэтому колонка узнается по заголовку, а неузнанную
// человек называет сам.
type NicheColumn string

// Колонки файла ниш.
const (
	NicheName    NicheColumn = "name"    // ниша, предмет, категория
	NicheSales   NicheColumn = "sales"   // продажи, шт в месяц
	NicheRevenue NicheColumn = "revenue" // выручка, ₽ в месяц
	NicheSellers NicheColumn = "sellers" // продавцов
	NichePrice   NicheColumn = "price"   // средняя цена, ₽
	NicheGrowth  NicheColumn = "growth"  // рост к прошлому году, доля: 0,12 — +12 %
	NicheTop10   NicheColumn = "top10"   // доля выручки у топ-10 продавцов, 0..1
)

var nicheColumns = []NicheColumn{NicheName, NicheSales, NicheRevenue, NicheSellers, NichePrice, NicheGrowth, NicheTop10, NicheMarketplace}

var nicheLabels = map[NicheColumn]string{
	NicheName:        "Ниша",
	NicheSales:       "Продажи, шт/мес",
	NicheRevenue:     "Выручка, ₽/мес",
	NicheSellers:     "Продавцов",
	NichePrice:       "Средняя цена, ₽",
	NicheGrowth:      "Рост к году",
	NicheTop10:       "Доля топ-10",
	NicheMarketplace: "Маркетплейсы",
}

// nicheHeaders — начала заголовков после FoldName. Узнается по началу, а не
// по вхождению: «Упущенная выручка» — не выручка. Проверка идет от длинного
// начала к короткому, поэтому «Выручка топ-10» — доля топ-10, а не выручка.
//
// ponytail: словарь из заголовков MPStats и обзоров, настоящих файлов
// продавца еще нет. Потолок — заголовок вне словаря; путь — человек называет
// колонку в окне, а новые написания дописываются сюда по живым файлам.
var nicheHeaders = []struct {
	prefix string
	col    NicheColumn
}{
	{"выручкатоп10", NicheTop10},
	{"долятоп10", NicheTop10},
	{"долявыручкитоп10", NicheTop10},
	{"топ10", NicheTop10},
	{"монопол", NicheTop10},
	{"изменениевыручки", NicheGrowth},
	{"динамика", NicheGrowth},
	{"рост", NicheGrowth},
	{"средняяцена", NichePrice},
	{"среднийчек", NichePrice},
	{"цена", NichePrice},
	{"продавц", NicheSellers},
	{"выручка", NicheRevenue},
	{"оборот", NicheRevenue},
	{"продажи", NicheSales},
	{"продаж", NicheSales},
	{"маркетплейс", NicheMarketplace},
	{"площадк", NicheMarketplace},
	{"ниша", NicheName},
	{"предмет", NicheName},
	{"категория", NicheName},
	{"название", NicheName},
}

func init() {
	// Длинное начало раньше короткого — иначе «выручка» съест «выручку топ-10».
	for i := range nicheHeaders {
		for j := i + 1; j < len(nicheHeaders); j++ {
			if strings.HasPrefix(nicheHeaders[j].prefix, nicheHeaders[i].prefix) {
				panic("nicheHeaders: " + nicheHeaders[j].prefix + " stands after its prefix " + nicheHeaders[i].prefix)
			}
		}
	}
}

// NicheColumns lists every column in the order the window shows them.
func NicheColumns() []NicheColumn { return append([]NicheColumn(nil), nicheColumns...) }

// Label is the column's name for a person.
func (c NicheColumn) Label() string { return nicheLabels[c] }

// ParseNicheColumn validates a column id the window sends back.
func ParseNicheColumn(raw string) (NicheColumn, error) {
	if _, ok := nicheLabels[NicheColumn(raw)]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownNicheColumn, raw)
	}
	return NicheColumn(raw), nil
}

// RecognizeNicheColumn guesses the column from a header of the file; false —
// не узнал, колонку называет человек.
func RecognizeNicheColumn(header string) (NicheColumn, bool) {
	h := FoldName(header)
	if h == "" {
		return "", false
	}
	// «Продажи, окт 2026» — продажи за месяц, а не продажи в месяц.
	if _, _, month := RecognizeNicheMonth(header); month {
		return "", false
	}
	for _, x := range nicheHeaders {
		if strings.HasPrefix(h, x.prefix) {
			return x.col, true
		}
	}
	return "", false
}

// Niche — ниша из выгрузки: название и те величины, что были в файле.
// Величины, которой в файле не было, у ниши нет — нулем она не притворяется.
type Niche struct {
	name    string
	values  map[NicheColumn]float64
	months  []MonthSales
	markets []Marketplace
}

// NewNiche checks the niche's rules and keeps its own copy of values.
func NewNiche(name string, values map[NicheColumn]float64) (*Niche, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: нет названия", ErrInvalidNiche)
	}
	for c, v := range values {
		switch c {
		case NicheName, NicheMarketplace:
			return nil, fmt.Errorf("%w: %s — не число", ErrInvalidNiche, c.Label())
		case NicheSales, NicheRevenue, NicheSellers, NichePrice:
			if v < 0 {
				return nil, fmt.Errorf("%w: «%s» %s меньше нуля", ErrInvalidNiche, name, c.Label())
			}
		case NicheTop10:
			if v < 0 || v > 1 {
				return nil, fmt.Errorf("%w: «%s» доля топ-10 вне 0-100 %%", ErrInvalidNiche, name)
			}
		case NicheGrowth:
			if v < -1 {
				return nil, fmt.Errorf("%w: «%s» падение больше 100 %%", ErrInvalidNiche, name)
			}
		default:
			return nil, fmt.Errorf("%w: %q", ErrUnknownNicheColumn, c)
		}
	}
	return &Niche{name: name, values: maps.Clone(values)}, nil
}

// Name is the niche's name.
func (n *Niche) Name() string { return n.name }

// Value returns a value and whether the file had it.
func (n *Niche) Value(c NicheColumn) (float64, bool) {
	v, ok := n.values[c]
	return v, ok
}
