package markets_test

import (
	"errors"
	"iter"
	"math"
	"strconv"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Выдуманная выгрузка MPStats «Товары категории» с теми же заголовками, что у
// настоящей: товар на строку, ниша — колонкой, период в файле не записан.
var itemsHeader = []string{"SKU", "Name", "Brand", "Seller", "Sales, pcs", "Revenue, ₽", "Sales per day average", "Niche Name"}

func itemsRows(rows ...[]string) iter.Seq2[[]string, error] {
	return func(yield func([]string, error) bool) {
		for _, r := range append([][]string{itemsHeader}, rows...) {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// closeTo compares to a relative tolerance: месячные величины — суммы на сотни тысяч.
func closeTo(a, b float64) bool { return math.Abs(a-b) < 1e-9*max(1, math.Abs(b)) }

func TestReadItems(t *testing.T) {
	const jackets, coats = "Одежда/Куртки мужские", "Одежда/Пальто мужские"
	tb, err := markets.ReadItems(itemsRows(
		[]string{"1", "Куртка А", "Альфа", "Продавец 1", "92", "920000", "1", jackets},
		[]string{"2", "Куртка Б", "Бета", "Продавец 2", "46", "230000", "0,5", jackets},
		[]string{"3", "Куртка В", "Гамма", "Продавец 3", "0", "0", "0", jackets}, // без продаж — не продавец ниши
		[]string{"4", "Куртка Г", "Альфа", "Продавец 1", "много", "1", "1", jackets},
		[]string{"5", "Пальто А", "Альфа", "Продавец 1", "184", "368000", "2", coats},
		[]string{"6", "Шапка", "Альфа", "Продавец 1", "10", "5000", "0,1", "0"}, // MPStats не отнес к нише
	))
	if err != nil {
		t.Fatalf("ReadItems: %v", err)
	}
	if tb.Items != 4 {
		t.Errorf("items = %d, want 4 read into niches", tb.Items)
	}
	if tb.PeriodDays != 92 {
		t.Errorf("period = %d days, want 92 (Sales / Sales per day average)", tb.PeriodDays)
	}
	if len(tb.Niches) != 2 || tb.Niches[0].Name() != jackets {
		t.Fatalf("niches = %d; want 2, jackets first by revenue", len(tb.Niches))
	}
	month := 365.25 / 12 / 92
	want := map[domain.NicheColumn]float64{
		domain.NicheSales: 138 * month, domain.NicheRevenue: 1150000 * month,
		domain.NicheSellers: 2, domain.NichePrice: 1150000.0 / 138, domain.NicheTop10: 1,
	}
	for c, w := range want {
		if v, ok := tb.Niches[0].Value(c); !ok || !closeTo(v, w) {
			t.Errorf("%s = %v (%v), want %v", c, v, ok, w)
		}
	}
	if _, ok := tb.Niches[0].Value(domain.NicheGrowth); ok {
		t.Error("growth: one export has no last year, the niche must not pretend")
	}
	if len(tb.Problems) != 2 || tb.Problems[0].Row != 5 {
		t.Fatalf("problems = %+v; want row 5 («много») and the products without a niche", tb.Problems)
	}
}

func TestReadItems_top10(t *testing.T) {
	var rows [][]string
	for i := range 11 { // 11 продавцов: первый с выручкой 1000, остальные по 100
		rev := "100"
		if i == 0 {
			rev = "1000"
		}
		rows = append(rows, []string{strconv.Itoa(i), "Куртка", "", "Продавец " + strconv.Itoa(i), "1", rev, "0,1", "Куртки"})
	}
	tb, err := markets.ReadItems(itemsRows(rows...))
	if err != nil || len(tb.Niches) != 1 {
		t.Fatalf("ReadItems: %v, %d niches", err, len(tb.Niches))
	}
	if v, _ := tb.Niches[0].Value(domain.NicheTop10); !closeTo(v, 1900.0/2000) {
		t.Errorf("top10 = %v, want 0.95", v)
	}
}

func TestReadItems_notItems(t *testing.T) {
	seq := func(yield func([]string, error) bool) { yield(export[0], nil) }
	if _, err := markets.ReadItems(seq); !errors.Is(err, markets.ErrNotItems) {
		t.Errorf("niche export: err = %v, want ErrNotItems", err)
	}
	broken := errors.New("диск")
	seq = func(yield func([]string, error) bool) { _ = yield(itemsHeader, nil) && yield(nil, broken) }
	if _, err := markets.ReadItems(seq); !errors.Is(err, broken) {
		t.Errorf("read error: err = %v, want it passed through", err)
	}
}

func TestReadItems_periodFromSteadySellers(t *testing.T) {
	// «Sales per day average» округлен до сотых: у товара с одной продажей
	// за 92 дня он 0,01, и деление дает 100 дней. Таких товаров в настоящей
	// выгрузке большинство (#580), период решают товары с продажами каждый день.
	rows := [][]string{{"1", "Куртка", "", "Продавец 1", "920", "920000", "10", "Куртки"}}
	for i := range 3 {
		rows = append(rows, []string{strconv.Itoa(i + 2), "Куртка", "", "Продавец 2", "1", "1000", "0,01", "Куртки"})
	}
	tb, err := markets.ReadItems(itemsRows(rows...))
	if err != nil || tb.PeriodDays != 92 {
		t.Errorf("period = %d (%v), want 92 from the steady seller", tb.PeriodDays, err)
	}
}
