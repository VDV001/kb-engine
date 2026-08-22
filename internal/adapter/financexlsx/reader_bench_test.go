package financexlsx_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// Разбор книги — самый дорогой известный шаг движка: одно чтение стоило около
// 74 мс, и ровно поэтому отрисовка терминала берёт снимок на входе, а не читает
// файл. Число это добывалось руками один раз; здесь оно становится замером.
//
// ⚠️ Фикстура собрана той же библиотекой, что читает, — то есть она заведомо в
// состоянии, которое библиотека узнаёт. Для СКОРОСТИ разбора этого достаточно,
// для поведения — нет: живой файл проверяется отдельно, на копии.
// ⚠️ Суммы выдуманы.
func benchWorkbook(b *testing.B, rows int) string {
	b.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	must := func(err error) {
		b.Helper()
		if err != nil {
			b.Fatal(err)
		}
	}
	must(f.SetSheetName("Sheet1", "Расходы"))
	must(f.SetCellValue("Расходы", "A1", "Учёт расходов"))
	must(f.SetCellValue("Расходы", "A2", "Дата"))
	cats := []string{"Еда", "Транспорт", "Подписки", "Прочее"}
	for i := range rows {
		r := i + 3
		must(f.SetCellValue("Расходы", fmt.Sprintf("A%d", r), time.Date(2026, time.Month(1+i%7), 1+i%28, 0, 0, 0, 0, time.UTC)))
		must(f.SetCellValue("Расходы", fmt.Sprintf("B%d", r), cats[i%len(cats)]))
		must(f.SetCellValue("Расходы", fmt.Sprintf("C%d", r), "Подкатегория"))
		must(f.SetCellValue("Расходы", fmt.Sprintf("D%d", r), "Место"))
		must(f.SetCellValue("Расходы", fmt.Sprintf("F%d", r), 100+float64(i%900)))
	}
	_, err := f.NewSheet("Доходы")
	must(err)
	must(f.SetCellValue("Доходы", "A2", "Дата"))
	must(f.SetCellValue("Доходы", "A3", time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Доходы", "B3", "Зарплата"))
	must(f.SetCellValue("Доходы", "D3", 1000))
	_, err = f.NewSheet("Счета")
	must(err)
	must(f.SetCellValue("Счета", "A2", "Банк"))
	must(f.SetCellValue("Счета", "A3", "Альфа-Банк"))
	must(f.SetCellValue("Счета", "B3", 1000.0))
	must(f.SetCellValue("Счета", "C3", time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)))
	p := filepath.Join(b.TempDir(), "ledger.xlsx")
	must(f.SaveAs(p))
	return p
}

func BenchmarkRead(b *testing.B) {
	now := func() time.Time { return time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC) }
	for _, rows := range []int{100, 700} {
		p := benchWorkbook(b, rows)
		b.Run(fmt.Sprintf("строк_%d", rows), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := financexlsx.Read(p, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
