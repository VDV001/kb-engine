package markets_test

import (
	"errors"
	"math"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Выдуманный отчет Ozon «Аналитика → По товарам» в том же устройстве, что настоящий:
// параметры, пустая строка, группы, заголовки, пояснения Ozon, итог, товары.
var ozonRows = [][]string{
	{"Период: 01.08.2026 – 31.10.2026"},
	{"Товар: 3"},
	{"Продавец: Тест"},
	{},
	{"Товары", "SKU", "Схема работы", "Продажи", "", "", "Воронка продаж", "", "", "", "", "", "", "", "", "Факторы продаж"},
	{"", "", "", "ABC-анализ по\nсумме заказов", "Заказано на сумму (по цене реализации)", "Динамика", "Заказано товаров", "Динамика", "Выкуплено товаров", "Отменено товаров (на дату заказа)", "Возвращено товаров (на дату заказа)", "Средняя цена", "ДРР", "Дней без остатка 16.10.2026 – 12.11.2026", "Остаток на конец периода", "Рекомендация по поставке на FBO", "Сколько товаров поставить", "Отзывы", "Рейтинг товара", "Дней в акциях"},
	{"", "", "", "По общей стоимости заказанных товаров.", "Общая стоимость заказанных товаров", "Динамика по сравнению", "Общее количество заказанных товаров, включая отмены и возвраты", "Динамика", "Сколько товаров не отменили и не вернули", "Количество товаров, у которых отменили заказ", "Количество товаров, которое вернули", "Средняя цена реализации", "Отношение расходов на продвижение", "Сколько дней товара не было на складах", "Сколько товаров доступно к продаже", "Оцениваем, хватает ли остатков", "Сколько нужно поставить", "Сколько отзывов", "Оценка товара", "Сколько дней в акциях"},
	{"Итого и среднее", "–", "–", "–", "1000000", "–", "220", "–", "70", "140", "10", "4800", "0.02", "12 из 28", "60", "–", "–", "30", "4.9", "9 из 92"},
	{"Куртка А", "1001", "FBS, FBO", "A", "500000", "–", "120", "–", "30", "85", "5", "4900", "0.2", "10 из 28", "3", "Срочно поставить", "40", "12", "4.2", "8 из 92"},
	{"Куртка Б", "1002", "FBO", "B", "400000", "–", "90", "–", "38", "50", "2", "4700", "0", "–", "50", "–", "–", "18", "5", "0 из 92"},
	{"Куртка В", "1003", "FBO", "C", "много", "–", "10", "–", "2", "5", "3", "4600", "0", "–", "7", "–", "–", "0", "0", "0 из 92"},
}

func TestReadOzonProducts(t *testing.T) {
	r, err := markets.ReadOzonProducts(ozonRows)
	if err != nil {
		t.Fatalf("ReadOzonProducts: %v", err)
	}
	if r.Period != "01.08.2026 – 31.10.2026" || r.PeriodDays != 92 {
		t.Errorf("period = %q, %d days", r.Period, r.PeriodDays)
	}
	if r.Total == nil || r.Total.Data().Ordered != 220 || r.Total.Data().DaysOut != 12 || r.Total.Data().DaysOutOf != 28 {
		t.Fatalf("total = %+v", r.Total)
	}
	if len(r.Products) != 2 || len(r.Problems) != 1 || r.Problems[0].Row != 11 {
		t.Fatalf("products = %d, problems = %+v; want 2 and row 11 («много»)", len(r.Products), r.Problems)
	}
	a := r.Products[0].Data()
	if a.SKU != "1001" || a.Ordered != 120 || a.BoughtOut != 30 || a.Cancelled != 85 || a.Returned != 5 || a.DRR != 0.2 ||
		a.Stock != 3 || !a.UrgentSupply || a.SupplyQty != 40 || a.Rating != 4.2 || a.Reviews != 12 || a.ABC != "A" || a.PromoDays != 8 || a.OrderedSum != 500000 {
		t.Errorf("product A = %+v", a)
	}
	if b := r.Products[1].Data(); b.DaysOut != 0 || b.UrgentSupply || b.DRR != 0 {
		t.Errorf("product B: dash must read as none: %+v", b)
	}
	if h := r.Hints["ordered"]; h != "Общее количество заказанных товаров, включая отмены и возвраты" {
		t.Errorf("hint from Ozon's own row = %q", h)
	}
}

func TestReadOzonProducts_notAReport(t *testing.T) {
	if _, err := markets.ReadOzonProducts(export); !errors.Is(err, markets.ErrNotOzonReport) {
		t.Errorf("niche export: err = %v, want ErrNotOzonReport", err)
	}
	// И наоборот: отчет магазина — не выгрузка ниш, «Ниша» называет его, а не читает мусор.
	if _, err := markets.ReadNiches(ozonRows, nil); !errors.Is(err, markets.ErrShopReport) {
		t.Errorf("shop report as niches: err = %v, want ErrShopReport", err)
	}
}

func TestReadOzonProducts_mergesSKU(t *testing.T) {
	// Ozon делит один SKU на несколько строк — по категории, бренду и схеме
	// (настоящий отчет #580: 48 строк на 12 SKU). Окно склеивает их в товар:
	// суммы складываются, худшее (срочно, дни без остатка) остается, рейтинг —
	// по отзывам, ДРР — по сумме заказов.
	rows := append([][]string{}, ozonRows...)
	rows = append(rows, []string{"Куртка А", "1001", "FBS", "A", "100000", "–", "20", "–", "10", "6", "1", "5000", "0.05", "12 из 28", "4", "–", "–", "4", "4.8", "2 из 92"})
	r, err := markets.ReadOzonProducts(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Products) != 2 {
		t.Fatalf("products = %d, want 2 (SKU 1001 once)", len(r.Products))
	}
	a := r.Products[0].Data()
	if a.SKU != "1001" || a.Ordered != 140 || a.BoughtOut != 40 || a.Cancelled != 91 || a.Returned != 6 || a.Stock != 7 || a.OrderedSum != 600000 ||
		a.Scheme != "FBO, FBS" || !a.UrgentSupply || a.SupplyQty != 40 || a.DaysOut != 12 || a.DaysOutOf != 28 || a.PromoDays != 8 {
		t.Errorf("merged = %+v", a)
	}
	if want := (4.2*12 + 4.8*4) / 16; math.Abs(a.Rating-want) > 1e-9 || a.Reviews != 16 {
		t.Errorf("rating = %v of %v reviews, want %v of 16", a.Rating, a.Reviews, want)
	}
	if want := (0.2*500000 + 0.05*100000) / 600000; math.Abs(a.DRR-want) > 1e-9 || math.Abs(a.AvgPrice-600000.0/140) > 1e-9 {
		t.Errorf("drr = %v, avg = %v", a.DRR, a.AvgPrice)
	}
}
