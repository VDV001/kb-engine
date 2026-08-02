package financexlsx_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// The balance sheet is the one place the engine has only ever read. Until it
// can write there too, the assistant in the chat keeps a second way into the
// workbook — openpyxl straight into the cells — and two writers of one file are
// how the file ends up describing two different truths.
func TestSetBalance_writesTheBalanceAndTheDate(t *testing.T) {
	path := workbookWithExtraColumn(t)
	clock := fixedClock(2026, 8, 2)

	if err := financexlsx.SetBalance(path, "Альфа-Банк", money(t, "1447,12"), clock); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}

	acc := accountOnSheet(t, path, "Альфа-Банк")
	if acc.Balance().Kopecks() != 144712 {
		t.Errorf("баланс = %d копеек, ожидалось 144712", acc.Balance().Kopecks())
	}
	if got := acc.Updated().Format(time.DateOnly); got != "2026-08-02" {
		t.Errorf("дата обновления = %s, ожидалось 2026-08-02", got)
	}
}

// A balance written onto the wrong bank is worse than one not written: the
// number looks plausible on both rows. The row is found by name, never by
// position — the skill in the chat addressed rows 3, 4 and 5 by number, and one
// inserted row would have moved every account one bank over.
func TestSetBalance_leavesTheOtherAccountsAlone(t *testing.T) {
	path := workbookWithExtraColumn(t)

	if err := financexlsx.SetBalance(path, "Альфа-Банк", money(t, "1447,12"), fixedClock(2026, 8, 2)); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}

	for _, bank := range []string{"Сбербанк", "Т-Банк"} {
		acc := accountOnSheet(t, path, bank)
		if acc.Balance().Kopecks() != 10000 {
			t.Errorf("%s: баланс = %d копеек, ожидалось нетронутое 10000", bank, acc.Balance().Kopecks())
		}
		if got := acc.Updated().Format(time.DateOnly); got != "2026-07-28" {
			t.Errorf("%s: дата = %s, ожидалась нетронутая 2026-07-28", bank, got)
		}
	}
}

// An unknown bank is a question, not a row to create. Adding it silently would
// put a name into the vocabulary that decides what counts as an account
// everywhere else in the book.
func TestSetBalance_refusesABankTheSheetDoesNotKnow(t *testing.T) {
	path := workbookWithExtraColumn(t)

	err := financexlsx.SetBalance(path, "Озон Банк", money(t, "500"), fixedClock(2026, 8, 2))
	if err == nil {
		t.Fatal("неизвестный банк записан, ожидался отказ")
	}
	// The refusal names what is on the sheet: knowing the name is wrong is
	// useless without knowing which names are right.
	for _, want := range []string{"Озон Банк", "Сбербанк", "Альфа-Банк", "Т-Банк"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
}

// The currency format is the owner's, kept by hand for four years. Writing a
// number that loses it makes the sheet worse in a way no test of the value
// would notice.
func TestSetBalance_keepsTheCellFormatting(t *testing.T) {
	path := workbookWithExtraColumn(t)
	// The formatting has to be put there first: the shared fixture leaves the
	// Счета sheet unstyled, and a test comparing one unstyled cell to another
	// passes whatever the code does — this one did.
	//
	// What it guards is the property, not one line of ours: the balance may be
	// rewritten by any means as long as the owner's currency and date formats
	// survive it. Today excelize keeps them on its own; a future version that
	// rebuilt the row instead would break this and say so.
	styleAccountCells(t, path)
	before := map[string]int{"B4": cellStyle(t, path, "Счета", "B4"), "C4": cellStyle(t, path, "Счета", "C4")}

	if err := financexlsx.SetBalance(path, "Альфа-Банк", money(t, "1447,12"), fixedClock(2026, 8, 2)); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}

	for cell, want := range before {
		if got := cellStyle(t, path, "Счета", cell); got != want {
			t.Errorf("стиль %s = %d, был %d — форматирование потеряно", cell, got, want)
		}
	}
}

// styleAccountCells puts the owner's currency and date formats onto the account
// row, the way the live workbook has them.
func styleAccountCells(t *testing.T, path string) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	rub := `#,##0.00" ₽"`
	money, err := f.NewStyle(&excelize.Style{CustomNumFmt: &rub})
	if err != nil {
		t.Fatalf("NewStyle money: %v", err)
	}
	dmy := "DD.MM.YYYY"
	date, err := f.NewStyle(&excelize.Style{CustomNumFmt: &dmy})
	if err != nil {
		t.Fatalf("NewStyle date: %v", err)
	}
	if err := f.SetCellStyle("Счета", "B3", "B5", money); err != nil {
		t.Fatalf("SetCellStyle balance: %v", err)
	}
	if err := f.SetCellStyle("Счета", "C3", "C5", date); err != nil {
		t.Fatalf("SetCellStyle updated: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// The date has to read as a date in the book, not as the number a date is
// underneath. This is checked through the formatted value rather than the style
// id, because that is what the owner sees when the file is opened — and because
// the style-id version of this test passed on the live workbook while the cell
// there had turned into 46236.69.
func TestSetBalance_theDateStillReadsAsADate(t *testing.T) {
	path := workbookWithExtraColumn(t)
	styleAccountCells(t, path)

	// A clock with a time of day, like the real one: writing the moment instead
	// of the day leaves a fraction in the cell, and the workbook shows it.
	clock := func() time.Time { return time.Date(2026, 8, 2, 16, 38, 0, 0, time.UTC) }
	if err := financexlsx.SetBalance(path, "Альфа-Банк", money(t, "1447,12"), clock); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}

	// The stored number is a whole day. A spreadsheet keeps a date as days since
	// 1900, so a moment lands as 46236.69 — and whether that shows as a date or
	// as 46236.69 then depends on a format the writer does not control.
	raw := rawCell(t, path, "Счета", "C4")
	if strings.Contains(raw, ".") {
		t.Errorf("в ячейке %s — баланс подтверждается днём, а записан момент", raw)
	}
	if got := formattedCell(t, path, "Счета", "C4"); !strings.Contains(got, "2026") {
		t.Errorf("дата в книге = %q — читается не как дата", got)
	}
}

// formattedCell returns the cell as the spreadsheet displays it, formatting
// applied — the value a person reads, not the number underneath.
func formattedCell(t *testing.T, path, sheet, cell string) string {
	t.Helper()
	return readCell(t, path, sheet, cell)
}

// rawCell returns the number stored in the cell, formatting ignored.
func rawCell(t *testing.T, path, sheet, cell string) string {
	t.Helper()
	return readCell(t, path, sheet, cell, excelize.Options{RawCellValue: true})
}

func readCell(t *testing.T, path, sheet, cell string, opts ...excelize.Options) string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	v, err := f.GetCellValue(sheet, cell, opts...)
	if err != nil {
		t.Fatalf("GetCellValue %s!%s: %v", sheet, cell, err)
	}
	return v
}

func fixedClock(y int, m time.Month, d int) func() time.Time {
	return func() time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
}

func money(t *testing.T, raw string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(raw)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", raw, err)
	}
	return m
}

// accountOnSheet reads one account back through the reader, so the test asserts
// on what the engine will see next time rather than on the raw cell.
func accountOnSheet(t *testing.T, path, bank string) domain.Account {
	t.Helper()
	led, err := financexlsx.Read(path, time.Now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, a := range led.Accounts {
		if a.Bank() == bank {
			return a
		}
	}
	t.Fatalf("счёт %q не найден на листе", bank)
	return domain.Account{}
}

func cellStyle(t *testing.T, path, sheet, cell string) int {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	id, err := f.GetCellStyle(sheet, cell)
	if err != nil {
		t.Fatalf("GetCellStyle %s!%s: %v", sheet, cell, err)
	}
	return id
}
