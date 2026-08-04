package finance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Записанную трату нельзя было исправить ничем, кроме правки файла руками — то
// есть той самой дверью, через которую в книгу однажды попала строка без id.
// Владелец пропустил счёт в форме и остался с записью, которая ни к какому
// банку не относится.
//
// Правка идёт через движок и по членам объекта: переданное поле меняется,
// непереданное остаётся. Пустая строка означает «флаг не передавали» — стирание
// это отдельное намерение, иначе любая правка суммы молча снесла бы заметку.

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func editable(t *testing.T) finance.Record {
	t.Helper()
	amount, err := domain.ParseMoney("322.00")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	now := func() time.Time { return day(2026, time.August, 3) }
	rec, err := finance.Add(finance.AddParams{
		Kind:        domain.KindExpense,
		Date:        day(2026, time.August, 3),
		Amount:      amount,
		Category:    "Транспорт",
		Subcategory: "Такси",
		Description: "такси до центра",
	}, func() string { return "01TESTID" }, now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}

func TestEditSetsTheFieldThatWasPassed(t *testing.T) {
	rec := editable(t)
	later := func() time.Time { return day(2026, time.August, 4) }

	got, err := finance.Edit(rec, finance.EditParams{Account: "Сбербанк"}, later)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got.Transaction().Account() != "Сбербанк" {
		t.Errorf("Account = %q, ожидался Сбербанк", got.Transaction().Account())
	}
	// Ревизия растёт: синк отличает изменённую запись от нетронутой именно так.
	if got.Rev() <= rec.Rev() {
		t.Errorf("Rev = %d, была %d — правка должна повышать ревизию",
			got.Rev(), rec.Rev())
	}
}

func TestEditLeavesUntouchedFieldsAlone(t *testing.T) {
	rec := editable(t)
	later := func() time.Time { return day(2026, time.August, 4) }

	got, err := finance.Edit(rec, finance.EditParams{Account: "Сбербанк"}, later)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	tx := got.Transaction()
	if tx.Description() != "такси до центра" {
		t.Errorf("заметка потеряна: %q", tx.Description())
	}
	if tx.Category() != "Транспорт" || tx.Subcategory() != "Такси" {
		t.Errorf("категория потеряна: %q · %q", tx.Category(), tx.Subcategory())
	}
	if tx.Amount().String() != "322.00" {
		t.Errorf("сумма изменилась: %q", tx.Amount().String())
	}
	if tx.ID() != rec.Transaction().ID() {
		t.Errorf("id изменился: %q, был %q", tx.ID(), rec.Transaction().ID())
	}
}

// Стирание — намерение, которое надо выразить отдельно, а не пустой строкой:
// иначе правка одного поля тихо очищала бы все остальные.
func TestEditClearsOnlyWhenAskedExplicitly(t *testing.T) {
	rec := editable(t)
	later := func() time.Time { return day(2026, time.August, 4) }

	got, err := finance.Edit(rec, finance.EditParams{ClearDescription: true}, later)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got.Transaction().Description() != "" {
		t.Errorf("заметка не стёрта: %q", got.Transaction().Description())
	}
}

// Правка без единого поля — почти наверняка опечатка в командной строке, и
// молча повышать ревизию на ней значит врать синку о том, что запись менялась.
func TestEditRefusesAnEmptyChange(t *testing.T) {
	rec := editable(t)
	later := func() time.Time { return day(2026, time.August, 4) }

	if _, err := finance.Edit(rec, finance.EditParams{}, later); !errors.Is(err, finance.ErrNothingToEdit) {
		t.Errorf("err = %v, ожидался ErrNothingToEdit", err)
	}
}

// Домен решает, что можно у дохода: категории и счёта у него нет, и правка не
// может стать дверью в обход этого правила.
func TestEditKeepsDomainRules(t *testing.T) {
	amount, err := domain.ParseMoney("90000.00")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	now := func() time.Time { return day(2026, time.August, 3) }
	income, err := finance.Add(finance.AddParams{
		Kind: domain.KindIncome, Date: day(2026, time.August, 3),
		Amount: amount, Source: "Зарплата",
	}, func() string { return "01INCOME" }, now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := finance.Edit(income, finance.EditParams{Account: "Сбербанк"}, now); err == nil {
		t.Error("счёт у дохода принят, а домен его не допускает")
	}
}
