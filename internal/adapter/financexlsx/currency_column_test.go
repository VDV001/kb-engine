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

// Колонка «Валюта» ищется по заголовку, как id, а не по номеру: восьмая колонка
// живой книги без заголовка держит счёт, девятая — id, и любой «следующий номер»
// легко попадает в чужую клетку. Рубль — пустая ячейка: книга, где валюты нет
// вовсе, читается как прежде и от рублёвых записей не меняется.

func foreignTx(t *testing.T, id, kind, code string) domain.Transaction {
	t.Helper()
	p := domain.TransactionParams{
		ID:     id,
		Kind:   kind,
		Date:   time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		Amount: domain.NewMoney(1230),
		Now:    writeClock,
	}
	if kind == domain.KindExpense {
		p.Category = "Сервисы"
	} else {
		p.Source = "Заказчик"
	}
	if code != "" {
		p.Currency = currency(t, code)
	}
	out, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

// headerColumn returns the 1-based column whose header row reads name, or 0.
func headerColumn(t *testing.T, path, sheet, name string) int {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	for i, v := range rows[1] {
		if strings.TrimSpace(v) == name {
			return i + 1
		}
	}
	return 0
}

func TestApplyRows_currencyColumn(t *testing.T) {
	cases := []struct {
		name  string
		sheet string
		kind  string
		// The id column of the paired fixture: I on Расходы (past the bank in H),
		// E on Доходы. The currency column has to land past it.
		idCol int
	}{
		{"трата", "Расходы", domain.KindExpense, 9},
		{"доход", "Доходы", domain.KindIncome, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := paired(t)
			if err := financexlsx.ApplyRows(path, []domain.Transaction{foreignTx(t, "01D", tc.kind, "USD")}, nil, writeClock); err != nil {
				t.Fatalf("ApplyRows: %v", err)
			}

			col := headerColumn(t, path, tc.sheet, "Валюта")
			if col == 0 {
				t.Fatalf("на листе %s нет заголовка «Валюта»", tc.sheet)
			}
			if col <= tc.idCol {
				t.Errorf("колонка валюты %d не правее id (%d): встала бы в чужую клетку", col, tc.idCol)
			}
			if tc.sheet == "Расходы" {
				if v := cellValue(t, path, "Расходы", "H4"); v != "Сбербанк" {
					t.Errorf("колонка H = %q, банк владельца тронут", v)
				}
			}

			got := readBack(t, path)
			if c := got["01D"].Currency().Code(); c != "USD" {
				t.Errorf("01D прочитана в %s, want USD", c)
			}
			for _, id := range []string{"01A", "01B", "01C"} {
				if c := got[id].Currency(); !c.IsBase() {
					t.Errorf("%s прочитана в %s, а записана рублями", id, c.Code())
				}
			}
		})
	}
}

// Рублёвая запись колонку не заводит: у книги владельца её нет, и первая же
// синхронизация не должна менять вид листа, если валюты в данных нет.
func TestApplyRows_roublesLeaveTheSheetWithoutACurrencyColumn(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path, []domain.Transaction{foreignTx(t, "01D", domain.KindExpense, "")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	if col := headerColumn(t, path, "Расходы", "Валюта"); col != 0 {
		t.Errorf("рублёвая запись завела колонку «Валюта» (%d)", col)
	}
}

// Запись, вернувшаяся к рублю, обязана очистить свою ячейку. Иначе следующее
// чтение увидит в книге старую валюту и сверка вернёт её в журнал.
func TestApplyRows_clearsTheCurrencyOfARowBackInRoubles(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path, []domain.Transaction{foreignTx(t, "01D", domain.KindExpense, "USD")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows USD: %v", err)
	}
	if err := financexlsx.ApplyRows(path, []domain.Transaction{foreignTx(t, "01D", domain.KindExpense, "")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows RUB: %v", err)
	}
	if c := readBack(t, path)["01D"].Currency(); !c.IsBase() {
		t.Errorf("01D осталась в %s после записи рублями", c.Code())
	}
}

// Удалённая строка уносит и свою валюту: оставшееся «USD» в пустой строке
// приняли бы за начало новой записи.
func TestApplyRows_removalClearsTheCurrencyCell(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path, []domain.Transaction{foreignTx(t, "01D", domain.KindExpense, "USD")}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	col := headerColumn(t, path, "Расходы", "Валюта")
	if col == 0 {
		t.Fatal("нет колонки «Валюта» — проверять удаление не на чем")
	}
	if err := financexlsx.ApplyRows(path, nil, []string{"01D"}, writeClock); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cell, _ := excelize.CoordinatesToCellName(col, 5)
	if v := cellValue(t, path, "Расходы", cell); v != "" {
		t.Errorf("после удаления в %s осталось %q", cell, v)
	}
}

// Код, набранный руками, читается без учёта регистра, а негодный — отказ, а не
// рубль: «не разобрал — значит рубль» показало бы долларовую трату рублёвой.
func TestRead_currencyColumn(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"строчные приводятся", "usd", "USD", false},
		{"пустая ячейка — рубль", "", "RUB", false},
		{"негодный код — отказ", "ДОЛ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := paired(t)
			setCell(t, path, "Расходы", "J2", "Валюта")
			setCell(t, path, "Расходы", "J3", tc.value)
			led, err := financexlsx.Read(path, writeClock)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrInvalidCurrency) {
					t.Fatalf("Read = %v, want ErrInvalidCurrency", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			for _, tx := range led.Transactions {
				if tx.ID() == "01A" && tx.Currency().Code() != tc.want {
					t.Errorf("01A прочитана в %s, want %s", tx.Currency().Code(), tc.want)
				}
			}
		})
	}
}

// Последний рубеж перед книгой. Запись могла попасть в журнал без книги под
// рукой — тогда валюту счёта не подставили и не сверили. Книга знает валюты
// счетов, и строку, которая противоречит своему счёту, она не примет: иначе
// доллары легли бы в рублёвый счёт молча.
func TestApplyRows_refusesACurrencyAgainstItsAccount(t *testing.T) {
	path := paired(t) // «Сбербанк» на листе «Счета» рублёвый
	p := domain.TransactionParams{
		ID: "01D", Kind: domain.KindExpense, Date: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		Amount: domain.NewMoney(1230), Category: "Сервисы", Account: "Сбербанк",
		Currency: currency(t, "USD"), Now: writeClock,
	}
	bad, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	err = financexlsx.ApplyRows(path, []domain.Transaction{bad}, nil, writeClock)
	if !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("ApplyRows = %v, want ErrCurrencyMismatch", err)
	}
	if _, ok := readBack(t, path)["01D"]; ok {
		t.Error("строка записана в книгу несмотря на отказ")
	}
}
