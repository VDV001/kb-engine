package financexlsx_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// Заведение счёта — отдельное намерение от записи баланса, и потому отдельная
// функция, а не флаг у SetBalance.
//
// SetBalance отказывает незнакомому имени намеренно: лист «Счета» — словарь,
// решающий, что считается счётом во всей книге, и опечатка в --bank завела бы
// туда слово, которое дальше читают все витрины. Отказ остаётся; здесь второй
// вход, которым человек говорит «я знаю, что такого счёта нет».
func TestAddAccount_appendsARowTheReaderSees(t *testing.T) {
	path := workbookWithExtraColumn(t)

	err := financexlsx.AddAccount(path, "Займ → Коллеге", money(t, "3000"), domain.Currency{}, domain.UnknownRate(), fixedClock(2026, 8, 4))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	acc := accountOnSheet(t, path, "Займ → Коллеге")
	if acc.Balance().Kopecks() != 300000 {
		t.Errorf("баланс = %d копеек, ожидалось 300000", acc.Balance().Kopecks())
	}
	if got := acc.Updated().Format(time.DateOnly); got != "2026-08-04" {
		t.Errorf("дата = %s, ожидалось 2026-08-04", got)
	}
	// Заведённый счёт обязан стать обычным счётом: со следующего раза его
	// баланс правится штатной командой, а не второй особенной.
	if err := financexlsx.SetBalance(path, "Займ → Коллеге", money(t, "2000"), fixedClock(2026, 8, 5)); err != nil {
		t.Fatalf("SetBalance по заведённому счёту: %v", err)
	}
}

// Строка встаёт следом за последним счётом, а не куда попало: между счетами и
// новой строкой не должно появиться пустой, иначе лист читается как два списка.
func TestAddAccount_putsTheRowRightAfterTheLastAccount(t *testing.T) {
	path := workbookWithExtraColumn(t)

	if err := financexlsx.AddAccount(path, "Займ → Коллеге", money(t, "3000"), domain.Currency{}, domain.UnknownRate(), fixedClock(2026, 8, 4)); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows("Счета")
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	// Фикстура держит три счёта в строках 3-5, новый обязан лечь в шестую.
	if len(rows) < 6 {
		t.Fatalf("на листе %d строк, новая не появилась", len(rows))
	}
	if got := strings.TrimSpace(rows[5][0]); got != "Займ → Коллеге" {
		t.Errorf("строка 6 = %q, ожидался новый счёт", got)
	}
}

// Имя, которое лист уже знает, — это не «заведи ещё раз», а сигнал, что
// предположение вызывающего неверно. Два счёта с одним смыслом разложат деньги
// по двум строкам, и обе будут выглядеть правдоподобно.
func TestAddAccount_refusesANameTheSheetAlreadyKnows(t *testing.T) {
	// Другое написание — тот же счёт. Побуквенное сравнение пропустило бы
	// «сбер банк» рядом со «Сбербанк», и это ровно тот разнобой, который
	// словарь быстрого ввода уже нашёл в живой книге.
	for _, name := range []string{"Сбербанк", "сбербанк", "  Альфа-Банк ", "т банк"} {
		t.Run(name, func(t *testing.T) {
			path := workbookWithExtraColumn(t)

			err := financexlsx.AddAccount(path, name, money(t, "500"), domain.Currency{}, domain.UnknownRate(), fixedClock(2026, 8, 4))
			if !errors.Is(err, financexlsx.ErrAccountExists) {
				t.Fatalf("AddAccount(%q) = %v, ожидался ErrAccountExists", name, err)
			}
			// Отказ называет, какое имя на листе уже стоит: без этого человек
			// не знает, спорит он с опечаткой или со своим же счётом.
			if !strings.Contains(err.Error(), "Сбербанк") && !strings.Contains(err.Error(), "Альфа-Банк") && !strings.Contains(err.Error(), "Т-Банк") {
				t.Errorf("отказ не назвал существующий счёт: %v", err)
			}
		})
	}
}

// Отказ обязан оставить книгу нетронутой: половина работы хуже, чем её
// отсутствие, потому что искать её никто не пойдёт.
func TestAddAccount_leavesTheBookAloneWhenItRefuses(t *testing.T) {
	path := workbookWithExtraColumn(t)
	before := accountsOnSheet(t, path)

	if err := financexlsx.AddAccount(path, "сбербанк", money(t, "500"), domain.Currency{}, domain.UnknownRate(), fixedClock(2026, 8, 4)); err == nil {
		t.Fatal("дубль записан, ожидался отказ")
	}

	after := accountsOnSheet(t, path)
	if len(before) != len(after) {
		t.Fatalf("счетов было %d, стало %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Bank() != after[i].Bank() || before[i].Balance().Kopecks() != after[i].Balance().Kopecks() {
			t.Errorf("счёт %d изменился: %s %s → %s %s",
				i, before[i].Bank(), before[i].Balance(), after[i].Bank(), after[i].Balance())
		}
	}
}

// Пустое имя проверяет домен, и проверка не дублируется здесь: копия правила в
// адаптере — это та копия, которая потом разойдётся с оригиналом.
func TestAddAccount_refusesABlankName(t *testing.T) {
	path := workbookWithExtraColumn(t)

	err := financexlsx.AddAccount(path, "   ", money(t, "500"), domain.Currency{}, domain.UnknownRate(), fixedClock(2026, 8, 4))
	if !errors.Is(err, domain.ErrInvalidAccount) {
		t.Fatalf("AddAccount(пустое имя) = %v, ожидался ErrInvalidAccount", err)
	}
}

// Формат ячейки — единственное, что отличает 3000 от «3 000,00 ₽» на листе,
// который владелец ведёт руками четыре года. Новая строка наследует его у
// соседней, иначе заведённый счёт видно по тому, что он выглядит чужим.
func TestAddAccount_inheritsTheFormattingOfTheRowAbove(t *testing.T) {
	path := workbookWithExtraColumn(t)
	// Фикстура оставляет лист «Счета» без оформления, поэтому его надо
	// поставить: наследовать пустое умеет и сломанная реализация.
	styleAccountsRow(t, path, 5)

	want := map[string]int{
		"B6": cellStyle(t, path, "Счета", "B5"),
		"C6": cellStyle(t, path, "Счета", "C5"),
	}
	if err := financexlsx.AddAccount(path, "Займ → Коллеге", money(t, "3000"), domain.Currency{}, domain.UnknownRate(), fixedClock(2026, 8, 4)); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	for cell, style := range want {
		if got := cellStyle(t, path, "Счета", cell); got != style {
			t.Errorf("%s: стиль = %d, ожидался унаследованный %d", cell, got, style)
		}
	}
}

// accountsOnSheet reads every account back through the reader.
func accountsOnSheet(t *testing.T, path string) []domain.Account {
	t.Helper()
	led, err := financexlsx.Read(path, time.Now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return led.Accounts
}

// styleAccountsRow puts the live sheet's currency and date formats onto one row
// of Счета, so that inheriting them is something a test can tell apart from
// inheriting nothing.
func styleAccountsRow(t *testing.T, path string, row int) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()

	moneyStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: new(`#,##0.00" ₽"`)})
	if err != nil {
		t.Fatalf("NewStyle: %v", err)
	}
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: new(`dd.mm.yyyy`)})
	if err != nil {
		t.Fatalf("NewStyle: %v", err)
	}
	balance, _ := excelize.CoordinatesToCellName(2, row)
	updated, _ := excelize.CoordinatesToCellName(3, row)
	if err := f.SetCellStyle("Счета", balance, balance, moneyStyle); err != nil {
		t.Fatalf("SetCellStyle: %v", err)
	}
	if err := f.SetCellStyle("Счета", updated, updated, dateStyle); err != nil {
		t.Fatalf("SetCellStyle: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}
