package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Себестоимость (#580): файлом или вводом, по артикулу, у каждого кабинета своя;
// «Магазин» кабинета считает на счет и прибыль товара до рекламы.
func TestMarketsCosts(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(t.TempDir(), "costs.csv")
	if err := os.WriteFile(csv, []byte("Артикул;Себестоимость\n100_46;350\n300_M;дорого\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	imp, err := s.ImportCosts(row.ID, csv)
	if err != nil || imp.Read != 1 || len(imp.Problems) != 1 || imp.Problems[0].Row != 3 {
		t.Fatalf("import = %+v, %v", imp, err)
	}
	if err := s.SetCost(row.ID, "100_46", "400"); err != nil { // ввод в карточке поверх файла
		t.Fatal(err)
	}
	if err := s.SetCost(row.ID, "200_L", "минус"); err == nil {
		t.Error("a cost that is not a number saved")
	}
	if err := s.SetCost("нет-такого", "100_46", "400"); err == nil {
		t.Error("a cost for an unknown cabinet saved")
	}
	srv := fakeOzon(t)
	v, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	// Брюки: продажа 12.11 — на счет 462,50, себестоимость 400, прибыль 62,50.
	p := v.Products[0]
	if p.SKU != "11" || p.Money == nil || p.Money.Units != 1 || p.Money.Total != 462.5 || !p.Money.HasCost || p.Money.Cost != 400 ||
		p.OnAccount != 462.5 || p.Profit == nil || *p.Profit != 62.5 {
		t.Errorf("trousers = %+v, money %+v", p, p.Money)
	}
	// Пижама без начислений и без себестоимости: прибыли нет, а не ноль.
	if q := v.Products[1]; q.Profit != nil {
		t.Errorf("pajamas profit = %v, want none", *q.Profit)
	}
	// Себестоимость живет рядом с настройками и переживает перезапуск; ключей там нет.
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "costs.json"))
	if err != nil || !strings.Contains(string(raw), "100_46") || strings.Contains(string(raw), "k-1") {
		t.Errorf("costs.json = %s, %v", raw, err)
	}
	if err := s.SetCost(row.ID, "100_46", ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.costsOf(row.ID); len(c) != 0 {
		t.Errorf("costs after clearing = %v", c)
	}
}
