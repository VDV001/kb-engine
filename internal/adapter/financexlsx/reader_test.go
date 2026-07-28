package financexlsx_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/xuri/excelize/v2"
)

// fixture builds a workbook shaped exactly like the real ledger: a title row,
// a header row, then data from row 3. Sheet and column layout mirror
// Учёт_финансов.xlsx.
func fixture(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// --- Расходы: Дата | Категория | Подкатегория | Место | Описание | Сумма | Источник
	must(f.SetSheetName("Sheet1", "Расходы"))
	must(f.SetCellValue("Расходы", "A1", "Учёт расходов"))
	must(f.SetCellValue("Расходы", "A2", "Дата"))
	must(f.SetCellValue("Расходы", "A3", "2026-03-29"))
	must(f.SetCellValue("Расходы", "B3", "Еда"))
	must(f.SetCellValue("Расходы", "C3", "Рестораны/кафе"))
	must(f.SetCellValue("Расходы", "D3", "Поль Бейкери"))
	must(f.SetCellValue("Расходы", "E3", "Кафе"))
	must(f.SetCellValue("Расходы", "F3", 500))
	must(f.SetCellValue("Расходы", "G3", "Чек"))
	// decimals must survive the trip
	must(f.SetCellValue("Расходы", "A4", "2026-03-30"))
	must(f.SetCellValue("Расходы", "B4", "Транспорт"))
	must(f.SetCellValue("Расходы", "F4", 202.45))
	// a blank row in the middle is normal in a hand-kept sheet
	must(f.SetCellValue("Расходы", "A6", "2026-04-01"))
	must(f.SetCellValue("Расходы", "B6", "Долги")) // category outside the reference sheet
	must(f.SetCellValue("Расходы", "F6", 1000))

	// --- Доходы: Дата | Источник | Описание | Сумма
	_, err := f.NewSheet("Доходы")
	must(err)
	must(f.SetCellValue("Доходы", "A2", "Дата"))
	must(f.SetCellValue("Доходы", "A3", "2026-03-30"))
	must(f.SetCellValue("Доходы", "B3", "Перевод от мамы"))
	must(f.SetCellValue("Доходы", "D3", 1300))

	// --- Счета: Банк | Остаток | Обновлено
	_, err = f.NewSheet("Счета")
	must(err)
	must(f.SetCellValue("Счета", "A2", "Банк"))
	must(f.SetCellValue("Счета", "A3", "Сбербанк"))
	must(f.SetCellValue("Счета", "B3", 166703.82))
	must(f.SetCellValue("Счета", "C3", "2026-07-28"))

	path := filepath.Join(t.TempDir(), "ledger.xlsx")
	must(f.SaveAs(path))
	return path
}

func clock() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }
}

func TestRead_transactions(t *testing.T) {
	led, err := financexlsx.Read(fixture(t), clock())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := len(led.Transactions); got != 4 {
		t.Fatalf("transactions = %d, want 4 (3 expenses + 1 income, blank row skipped)", got)
	}

	first := led.Transactions[0]
	if first.Category() != "Еда" || first.Amount().Kopecks() != 50000 {
		t.Errorf("first = %s / %d kopecks, want Еда / 50000", first.Category(), first.Amount().Kopecks())
	}
	if !first.IsExpense() {
		t.Error("row from Расходы must be an expense")
	}

	// 202.45 must land as 20245 kopecks exactly, not 20244 or 20245.000001.
	if got := led.Transactions[1].Amount().Kopecks(); got != 20245 {
		t.Errorf("decimal amount = %d kopecks, want 20245", got)
	}

	// A category absent from the reference sheet must still import.
	if got := led.Transactions[2].Category(); got != "Долги" {
		t.Errorf("third category = %q, want Долги", got)
	}
}

func TestRead_accountsAndBalance(t *testing.T) {
	led, err := financexlsx.Read(fixture(t), clock())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := len(led.Accounts); got != 1 {
		t.Fatalf("accounts = %d, want 1", got)
	}
	if got := led.Accounts[0].Balance().Kopecks(); got != 16670382 {
		t.Errorf("balance = %d kopecks, want 16670382", got)
	}
	if got := led.TotalBalance().Kopecks(); got != 16670382 {
		t.Errorf("TotalBalance = %d kopecks, want 16670382", got)
	}
}

func TestRead_netIsExactSum(t *testing.T) {
	led, err := financexlsx.Read(fixture(t), clock())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// income 1300 − expenses (500 + 202.45 + 1000) = −402.45 → −40245 kopecks
	if got := led.Net().Kopecks(); got != -40245 {
		t.Errorf("Net = %d kopecks, want -40245", got)
	}
}

func TestRead_missingFile(t *testing.T) {
	if _, err := financexlsx.Read(filepath.Join(t.TempDir(), "nope.xlsx"), clock()); err == nil {
		t.Fatal("expected an error for a missing workbook, got nil")
	}
}
