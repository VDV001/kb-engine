package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Так пришёл дубль 140 ₽: одна и та же трата попала в книгу дважды — один раз
// через движок (со своим id), другой раз прямой записью в ячейки, мимо него.
// У второй строки id нет, поэтому сопоставить их по id невозможно, и синк
// принимал её как новую запись.
//
// Молча принять — значит записать в ledger вторую копию и закрепить ошибку.
// Молча пропустить — значит потерять строку, если она всё-таки настоящая.
// Поэтому строка называется, а решение остаётся за человеком.
func TestRepeatsFromWorkbook_findsARowThatRepeatsALedgerEntry(t *testing.T) {
	existing := expenseRecord(t, addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", ""))
	// Строка книги без id: движок раздаёт таким позиционное имя вида expense-r1179.
	row := workbookRow(t, "expense-r1179", "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк")

	got := finance.RepeatsFromWorkbook([]finance.Record{existing}, []domain.Transaction{row})

	if len(got) != 1 {
		t.Fatalf("найдено %d повторов, ожидался 1", len(got))
	}
	if got[0].Existing.Transaction().ID() != existing.Transaction().ID() {
		t.Errorf("повтор указывает на %s, ожидалась %s", got[0].Existing.Transaction().ID(), existing.Transaction().ID())
	}
	if got[0].Row.ID() != "expense-r1179" {
		t.Errorf("не названа строка книги: %s", got[0].Row.ID())
	}
}

// Строка со своим id — это та же запись, а не повтор: синк узнаёт её по id и
// обновляет. Считать её повтором значило бы объявить проблемой каждую строку,
// которую движок сам туда и записал.
func TestRepeatsFromWorkbook_ignoresARowTheLedgerAlreadyKnowsByID(t *testing.T) {
	existing := expenseRecord(t, addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", ""))
	row := workbookRow(t, existing.Transaction().ID(), "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк")

	if got := finance.RepeatsFromWorkbook([]finance.Record{existing}, []domain.Transaction{row}); len(got) != 0 {
		t.Errorf("строка со своим id принята за повтор: %d", len(got))
	}
}

// Строка без id, которой в ledger нет вовсе, — это законная ручная запись в
// книгу, и синк должен её принять. Защита ловит повтор, а не сам факт записи
// в книгу руками.
func TestRepeatsFromWorkbook_leavesAGenuinelyNewHandwrittenRowAlone(t *testing.T) {
	existing := expenseRecord(t, addParams(t, "2026-08-02", "140", "Здоровье", "Аптека", "Живика", "Альфа-Банк", ""))
	row := workbookRow(t, "expense-r1180", "2026-08-02", "250", "Еда", "Продукты", "Монетка", "Сбербанк")

	if got := finance.RepeatsFromWorkbook([]finance.Record{existing}, []domain.Transaction{row}); len(got) != 0 {
		t.Errorf("новая ручная строка принята за повтор: %d", len(got))
	}
}

func workbookRow(t *testing.T, id, date, amount, cat, sub, place, account string) domain.Transaction {
	t.Helper()
	m, err := domain.ParseMoney(amount)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", amount, err)
	}
	when, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: id, Kind: domain.KindExpense, Date: when, Amount: m,
		Category: cat, Subcategory: sub, Place: place, Account: account,
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	return tx
}
