package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Исключение переводов между своими счетами нигде не было видно.
//
// Правило верное: перекладывание денег со счёта на счёт — не трата. Но
// проверить его сходимость можно было только руками, повторив правило движка в
// уме: сумма строк в журнале не совпадает с итогом, и почему — на экране не
// сказано. Так однажды и вышла ложная тревога о расхождении.
//
// Отсюда требование: исключённое считается и называется. «Ничего не исключено»
// и «исключено на 2 000» — разные ответы, и второй должен быть виден.
func TestSummarize_countsWhatItExcluded(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) }
	mk := func(id, category, subcategory, amount string) finance.Record {
		t.Helper()
		m, err := domain.ParseMoney(amount)
		if err != nil {
			t.Fatalf("ParseMoney: %v", err)
		}
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: id, Kind: domain.KindExpense, Date: now(), Amount: m,
			Category: category, Subcategory: subcategory, Account: "Сбербанк", Now: now,
		})
		if err != nil {
			t.Fatalf("NewTransaction: %v", err)
		}
		rec, err := finance.NewRecord(tx, 1, now())
		if err != nil {
			t.Fatalf("NewRecord: %v", err)
		}
		return rec
	}

	s := finance.Summarize([]finance.Record{
		mk("01A", "Еда", "Продукты", "500.00"),
		mk("01B", "Прочее", "Перевод себе", "2000.00"),
		mk("01C", "Прочее", "Перевод себе", "1000.00"),
	})

	if s.Expenses.String() != "500.00" {
		t.Errorf("в расходы попал перевод: %s", s.Expenses)
	}
	if s.ExcludedTransferCount != 2 {
		t.Errorf("исключённых переводов насчитано %d, ожидалось 2", s.ExcludedTransferCount)
	}
	if s.ExcludedTransfers.String() != "3000.00" {
		t.Errorf("сумма исключённого = %s, ожидалось 3000.00", s.ExcludedTransfers)
	}
}

// Период без переводов не должен ничего сообщать: строка, которая горит
// всегда, перестаёт читаться.
func TestSummarize_saysNothingWhenNothingWasExcluded(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) }
	m, _ := domain.ParseMoney("500.00")
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: "01A", Kind: domain.KindExpense, Date: now(), Amount: m,
		Category: "Еда", Account: "Сбербанк", Now: now,
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	rec, _ := finance.NewRecord(tx, 1, now())

	s := finance.Summarize([]finance.Record{rec})
	if s.ExcludedTransferCount != 0 || !s.ExcludedTransfers.IsZero() {
		t.Errorf("на чистом периоде объявлено исключение: %d на %s",
			s.ExcludedTransferCount, s.ExcludedTransfers)
	}
}
