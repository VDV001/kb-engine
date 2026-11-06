package financexlsx_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// Обмен валюты в книге — одна строка «Расходов» и три колонки по заголовку:
// «Получено», «Валюта получения», «Счёт получения» (решение владельца
// 06.11.2026, #432). Правка строки в Excel не теряет вторую сторону, потому
// что она в той же строке. Колонки ищутся по заголовку, как «Валюта» и id.

// exchangeBook is the paired fixture plus a dollar account to receive on.
// Имена счетов выдуманные, суммы тоже (гейт owner-data).
func exchangeBook(t *testing.T) string {
	t.Helper()
	path := paired(t)
	setCell(t, path, "Счета", "A4", "Wise")
	setCellNum(t, path, "Счета", "B4", 50)
	setCell(t, path, "Счета", "D4", "USD")
	return path
}

func exchangeTx(t *testing.T, id, account string, cur domain.Currency, kopecks int64, recv domain.ReceivedParams) domain.Transaction {
	t.Helper()
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:          id,
		Kind:        domain.KindExpense,
		Date:        time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(kopecks),
		Currency:    cur,
		Category:    "Прочее",
		Subcategory: "Перевод себе",
		Account:     account,
		Received:    recv,
		Now:         writeClock,
	})
	if err != nil {
		t.Fatalf("build exchange: %v", err)
	}
	return out
}

// cellAt reads a cell by column number and row.
func cellAt(t *testing.T, path, sheet string, col, row int) string {
	t.Helper()
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		t.Fatal(err)
	}
	return cellValue(t, path, sheet, name)
}

func rublesToDollars(t *testing.T, id string) domain.Transaction {
	t.Helper()
	return exchangeTx(t, id, "Сбербанк", domain.Currency{}, 900000,
		domain.ReceivedParams{Amount: domain.NewMoney(10000), Currency: currency(t, "USD"), Account: "Wise"})
}

func TestApplyRows_exchangeRoundTrip(t *testing.T) {
	path := exchangeBook(t)
	if err := financexlsx.ApplyRows(path, []domain.Transaction{rublesToDollars(t, "01X")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	cols := map[string]int{}
	for _, h := range []string{"Получено", "Валюта получения", "Счёт получения"} {
		c := headerColumn(t, path, "Расходы", h)
		if c == 0 {
			t.Fatalf("нет заголовка «%s»", h)
		}
		if c <= 9 {
			t.Errorf("«%s» в колонке %d, не правее id (9): встала бы в чужую клетку", h, c)
		}
		cols[h] = c
	}
	if len(map[int]bool{cols["Получено"]: true, cols["Валюта получения"]: true, cols["Счёт получения"]: true}) != 3 {
		t.Fatalf("колонки обмена делят клетку: %v", cols)
	}

	got, ok := readBack(t, path)["01X"].Received()
	if !ok {
		t.Fatal("вторая сторона обмена не прочиталась")
	}
	if got.Amount() != domain.NewMoney(10000) || got.Currency().Code() != "USD" || got.Account() != "Wise" {
		t.Fatalf("Received = %v %s %s", got.Amount(), got.Currency().Code(), got.Account())
	}
	// Обычная трата обмена не получает: пустые клетки — не вторая сторона.
	if _, ok := readBack(t, path)["01A"].Received(); ok {
		t.Error("у обычной траты появилась вторая сторона")
	}
}

// Человек правит сумму в Excel — вторая сторона в той же строке остается.
func TestRead_exchangeSurvivesAnEditOfTheRow(t *testing.T) {
	path := exchangeBook(t)
	if err := financexlsx.ApplyRows(path, []domain.Transaction{rublesToDollars(t, "01X")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	setCellNum(t, path, "Расходы", "F5", 9100)
	tx := readBack(t, path)["01X"]
	if tx.Amount() != domain.NewMoney(910000) {
		t.Fatalf("сумма = %v: правка не та строка", tx.Amount())
	}
	if r, ok := tx.Received(); !ok || r.Account() != "Wise" {
		t.Fatalf("после правки суммы вторая сторона = %+v, %v", r, ok)
	}
}

// Удаление записи чистит и вторую сторону: иначе строка-призрак с «Получено».
func TestApplyRows_removalClearsTheExchange(t *testing.T) {
	path := exchangeBook(t)
	if err := financexlsx.ApplyRows(path, []domain.Transaction{rublesToDollars(t, "01X")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if err := financexlsx.ApplyRows(path, nil, []string{"01X"}, writeClock); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, h := range []string{"Получено", "Валюта получения", "Счёт получения"} {
		col := headerColumn(t, path, "Расходы", h)
		if v := cellAt(t, path, "Расходы", col, 5); v != "" {
			t.Errorf("«%s» после удаления = %q", h, v)
		}
	}
}

// Обмен из долларов в рубли в книге без «Валюты»: в один проход нужны и
// колонка валюты, и три колонки обмена — четыре разные клетки.
func TestApplyRows_currencyAndExchangeColumnsInOnePass(t *testing.T) {
	path := exchangeBook(t)
	back := exchangeTx(t, "01Y", "Wise", currency(t, "USD"), 5000,
		domain.ReceivedParams{Amount: domain.NewMoney(450000), Account: "Сбербанк"})
	if err := financexlsx.ApplyRows(path, []domain.Transaction{back}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	seen := map[int]string{}
	for _, h := range []string{"Валюта", "Получено", "Валюта получения", "Счёт получения"} {
		c := headerColumn(t, path, "Расходы", h)
		if c == 0 {
			t.Fatalf("нет заголовка «%s»", h)
		}
		if prev, dup := seen[c]; dup {
			t.Fatalf("«%s» и «%s» в одной колонке %d", h, prev, c)
		}
		seen[c] = h
	}
	got := readBack(t, path)["01Y"]
	if r, ok := got.Received(); !ok || !r.Currency().IsBase() || r.Amount() != domain.NewMoney(450000) {
		t.Fatalf("Received = %+v, %v", r, ok)
	}
	if got.Currency().Code() != "USD" {
		t.Fatalf("валюта списания = %s", got.Currency().Code())
	}
}

// Доллары, пришедшие на рублевый счет, — опечатка: отказ до записи книги.
func TestApplyRows_refusesReceivedOnAnAccountInAnotherCurrency(t *testing.T) {
	path := exchangeBook(t)
	bad := exchangeTx(t, "01Z", "Wise", currency(t, "USD"), 5000,
		domain.ReceivedParams{Amount: domain.NewMoney(5000), Currency: currency(t, "USD"), Account: "Сбербанк"})
	err := financexlsx.ApplyRows(path, []domain.Transaction{bad}, nil, writeClock)
	if !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("err = %v, want ErrCurrencyMismatch", err)
	}
	if _, ok := readBack(t, path)["01Z"]; ok {
		t.Error("запись с ошибкой валюты попала в книгу")
	}
}
