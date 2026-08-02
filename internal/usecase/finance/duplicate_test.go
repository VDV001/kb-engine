package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Один и тот же расход, записанный дважды, — это то, что случается само:
// сессия оборвалась на полпути, человек повторил, и в книге стало две строки
// по 140 ₽. Заметить это можно только глазом на витрине, через день или через
// месяц, и разобрать уже трудно — какая из двух настоящая, не знает никто.
//
// Поэтому вопрос задаётся в момент записи, когда ответ ещё очевиден.
func TestDuplicate_findsTheSameExpenseOnTheSameDay(t *testing.T) {
	existing := expenseRecord(t, addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", ""))

	got := finance.Duplicate([]finance.Record{existing}, addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", ""))

	if got == nil {
		t.Fatal("повтор не найден")
	}
	if got.Transaction().ID() != existing.Transaction().ID() {
		t.Errorf("найдена запись %s, ожидалась %s", got.Transaction().ID(), existing.Transaction().ID())
	}
}

// Две одинаковые траты в один день — законная жизнь, а не ошибка: два пакета
// минут в самокате, две поездки по одному тарифу. Их различает описание, и
// пока оно различается, движок молчит.
func TestDuplicate_allowsTwoSimilarExpensesToldApartByTheirNote(t *testing.T) {
	existing := expenseRecord(t, addParams(t, "2026-07-31", "96", "Транспорт", "Самокат", "Юрент", "Сбербанк", "Пакет минут"))

	got := finance.Duplicate([]finance.Record{existing}, addParams(t, "2026-07-31", "96", "Транспорт", "Самокат", "Юрент", "Сбербанк", "Страховка"))

	if got != nil {
		t.Errorf("две разные траты приняты за повтор: %s", got.Transaction().Description())
	}
}

// Совпадение ищется по всему, что различает трату. Разный день, разная сумма,
// разный счёт — разные траты.
func TestDuplicate_comparesEveryFieldThatTellsExpensesApart(t *testing.T) {
	base := addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", "")
	existing := expenseRecord(t, base)

	for _, c := range []struct {
		name string
		p    finance.AddParams
	}{
		{"другой день", addParams(t, "2026-08-01", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", "")},
		{"другая сумма", addParams(t, "2026-08-02", "141", "Здоровье", "Аптека", "Живика", "Альфа-Банк", "")},
		{"другое место", addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Монетка", "Альфа-Банк", "")},
		{"другой счёт", addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Сбербанк", "")},
		{"другая категория", addParams(t, "2026-08-02", "140", "Еда", "Аптека", "Живика", "Альфа-Банк", "")},
	} {
		if got := finance.Duplicate([]finance.Record{existing}, c.p); got != nil {
			t.Errorf("%s: принято за повтор", c.name)
		}
	}
}

// Пустая дата означает «сегодня», и повтор ищется по тому дню, в который
// запись реально ляжет. Иначе защита отключается ровно в самом частом случае —
// когда дату не пишут.
func TestDuplicate_resolvesAnEmptyDateToTheSameDayTheRecordWouldGet(t *testing.T) {
	today := time.Now().UTC().Format(time.DateOnly)
	existing := expenseRecord(t, addParams(t, today, "418", "Транспорт", "Такси", "Яндекс Такси", "Сбербанк", ""))

	p := addParams(t, "", "418", "Транспорт", "Такси", "Яндекс Такси", "Сбербанк", "")
	if got := finance.Duplicate([]finance.Record{existing}, p); got == nil {
		t.Error("повтор без указанной даты не найден")
	}
}

func addParams(t *testing.T, date, amount, cat, sub, place, account, note string) finance.AddParams {
	t.Helper()
	m, err := domain.ParseMoney(amount)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", amount, err)
	}
	var when time.Time
	if date != "" {
		if when, err = time.Parse(time.DateOnly, date); err != nil {
			t.Fatalf("parse date %q: %v", date, err)
		}
	}
	return finance.AddParams{
		Kind: domain.KindExpense, Date: when, Amount: m,
		Category: cat, Subcategory: sub, Place: place, Account: account, Description: note,
	}
}

func expenseRecord(t *testing.T, p finance.AddParams) finance.Record {
	t.Helper()
	rec, err := finance.Add(p, func() string { return "01TEST" }, time.Now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}
