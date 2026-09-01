package financexlsx_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// setCellNum пишет ЧИСЛОВУЮ ячейку, в отличие от соседнего setCell.
//
// Разница не косметическая: в живой книге курс набирают числом, и excelize
// отдаёт его как "84.28000000000001". Проверив только текстовую ячейку, тест
// зеленел бы на разборе, который на настоящей книге ошибается.
func setCellNum(t *testing.T, path, sheet, cell string, value float64) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	if err := f.SetCellValue(sheet, cell, value); err != nil {
		t.Fatalf("set %s!%s: %v", sheet, cell, err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = f.Close()
}

func accountsByName(t *testing.T, path string) map[string]domain.Account {
	t.Helper()
	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	out := map[string]domain.Account{}
	for _, a := range led.Accounts {
		out[a.Bank()] = a
	}
	return out
}

// Книга владельца существует в единственном экземпляре, и колонок валюты в ней
// нет. Пункт приёмки #332: старая книга читается, счета не теряются, поведение
// прежнее.
//
// Это главный тест среза. Всё остальное можно доделать позже; книга, которая
// перестала читаться, — потеря данных.
func TestRead_oldWorkbookWithoutCurrencyColumns(t *testing.T) {
	accounts := accountsByName(t, workbookWithExtraColumn(t))

	if len(accounts) != 3 {
		t.Fatalf("счетов прочитано %d, ожидалось 3 — счета теряются", len(accounts))
	}
	for name, acc := range accounts {
		if !acc.Currency().IsBase() {
			t.Errorf("%s: Currency() = %q, у старой книги все счета рублёвые", name, acc.Currency().Code())
		}
		if acc.Rate().Known() {
			t.Errorf("%s: у счёта из старой книги взялся курс — его там неоткуда взять", name)
		}
		value, ok := acc.BaseValue()
		if !ok || value.Kopecks() != 10000 {
			t.Errorf("%s: BaseValue() = %d, %v; ожидалось 10000 и true", name, value.Kopecks(), ok)
		}
	}
}

// Валюта и курс читаются из четвёртой и пятой колонок. Сумма остаётся в своей
// валюте, курс лежит рядом — а не одна пересчитанная цифра.
func TestRead_currencyAndRateColumns(t *testing.T) {
	path := workbookWithExtraColumn(t)
	setCell(t, path, "Счета", "D3", "USD")
	setCellNum(t, path, "Счета", "E3", 84.28)

	acc := accountsByName(t, path)["Сбербанк"]
	if acc.Currency().Code() != "USD" {
		t.Fatalf("Currency() = %q, ожидалось USD", acc.Currency().Code())
	}
	if acc.Balance().Kopecks() != 10000 {
		t.Errorf("Balance() = %d, ожидалось 10000 — сумма остаётся в своей валюте", acc.Balance().Kopecks())
	}
	per, ok := acc.Rate().PerUnit()
	if !ok || per.Kopecks() != 8428 {
		t.Errorf("курс = %d, %v; ожидалось 8428 копеек и true", per.Kopecks(), ok)
	}
	// 100,00 × 84,28 = 8 428,00 ₽
	value, ok := acc.BaseValue()
	if !ok || value.Kopecks() != 842800 {
		t.Errorf("BaseValue() = %d, %v; ожидалось 842800 и true", value.Kopecks(), ok)
	}
}

// Валюта названа, курс не назван — «неизвестно», а не ноль и не единица.
// Пустая ячейка курса встречается чаще всего: наличные, полученные подарком,
// курса не имеют вовсе.
func TestRead_currencyWithoutRateStaysUnknown(t *testing.T) {
	path := workbookWithExtraColumn(t)
	setCell(t, path, "Счета", "D3", "TRY")

	acc := accountsByName(t, path)["Сбербанк"]
	if acc.Currency().Code() != "TRY" {
		t.Fatalf("Currency() = %q, ожидалось TRY", acc.Currency().Code())
	}
	if acc.Rate().Known() {
		t.Error("курс объявлен известным при пустой ячейке")
	}
	if _, ok := acc.BaseValue(); ok {
		t.Error("счёт без курса оценён — витрина покажет выдуманное число как замер")
	}
	if acc.Balance().Kopecks() != 10000 {
		t.Errorf("Balance() = %d, ожидалось 10000 — неизвестен курс, а не остаток", acc.Balance().Kopecks())
	}
}

// Негодное значение в колонке валюты — отказ с номером строки, а не молчаливое
// «считаем рублёвым». Молча проигнорировав его, движок показал бы валютный
// счёт рублёвым, то есть ровно тот дефект, ради которого заведена #332.
func TestRead_brokenCurrencyIsRefusedWithRow(t *testing.T) {
	path := workbookWithExtraColumn(t)
	setCell(t, path, "Счета", "D3", "рубли")

	_, err := financexlsx.Read(path, writeClock)
	if err == nil {
		t.Fatal("негодная валюта принята молча")
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Errorf("ошибка не называет строку: %v", err)
	}
	if !strings.Contains(err.Error(), "Счета") {
		t.Errorf("ошибка не называет лист: %v", err)
	}
}

// Курс без валюты — противоречие: оценивать в рублях рубли не по единице
// нельзя, и такая строка почти наверняка означает съехавшую колонку.
func TestRead_rateWithoutCurrencyIsRefused(t *testing.T) {
	path := workbookWithExtraColumn(t)
	setCellNum(t, path, "Счета", "E3", 84.28)

	if _, err := financexlsx.Read(path, writeClock); err == nil {
		t.Fatal("курс у рублёвого счёта принят молча")
	}
}
