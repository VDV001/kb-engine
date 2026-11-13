package main

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestReadShopFile(t *testing.T) {
	rows := [][]any{
		{"Период: 01.08.2026 – 31.10.2026"},
		{},
		{"Товары", "SKU", "Схема работы", "Продажи"},
		{"", "", "", "Заказано товаров", "Выкуплено товаров", "Отменено товаров (на дату заказа)", "Возвращено товаров (на дату заказа)", "Остаток на конец периода", "Рекомендация по поставке на FBO", "Рейтинг товара", "Отзывы", "Дней без остатка 16.10.2026 – 12.11.2026"},
		{"", "", "", "Общее количество заказанных товаров", "Сколько товаров не отменили и не вернули", "Отменили", "Вернули", "Доступно к продаже", "Хватает ли остатков", "Оценка", "Сколько отзывов", "Сколько дней не было"},
		{"Итого и среднее", "–", "–", 130, 40, 85, 5, 10, "–", 4.5, 12, "10 из 28"},
		{"Куртка А", "1001", "FBO", 120, 30, 85, 5, 3, "Срочно поставить", 4.2, 12, "10 из 28"},
		{"Куртка Б", "1002", "FBO", 10, 10, 0, 0, 7, "–", 0, 0, "–"},
	}
	f := excelize.NewFile()
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(t.TempDir(), "отчет.xlsx")
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	v, err := readShopFile(p)
	if err != nil {
		t.Fatalf("readShopFile: %v", err)
	}
	if v.File != "отчет.xlsx" || v.Period != "01.08.2026 – 31.10.2026" || v.PeriodDays != 92 || len(v.Products) != 2 {
		t.Fatalf("view = %+v", v)
	}
	a := v.Products[0]
	if a.SKU != "1001" || a.Ordered != 120 || a.Buyout != 0.25 || a.Cancel != 0.7083 || !a.Urgent || len(a.Issues) != 3 || a.Issues[0] != "cancels" {
		t.Errorf("product A = %+v", a)
	}
	if v.Total.Ordered != 130 || v.Total.DaysOut != 10 || v.Total.DaysOutOf != 28 {
		t.Errorf("total = %+v", v.Total)
	}
	if v.Hints["ordered"] != "Общее количество заказанных товаров" {
		t.Errorf("hints = %v", v.Hints)
	}
	if v.Counts["cancels"] != 1 || v.Counts["stock"] != 1 {
		t.Errorf("counts = %v", v.Counts)
	}
	if _, err := readNicheFile(p, nil, testNow); err == nil {
		t.Error("shop report read as niches")
	}
}
