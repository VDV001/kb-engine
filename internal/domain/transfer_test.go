package domain_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Перевод между своими счетами — не трата и не доход: деньги не пришли и не
// ушли, они переложены. Сложить их с настоящими расходами значит сказать, что
// человек потратил больше, чем потратил.
//
// Правило до сих пор жило в питоновском скрипте сборки дашборда и больше нигде,
// поэтому веб показывал 275 015,96, а движок 277 015,96 — расхождение ровно на
// одну запись. Два счёта одних денег: чей прав, по числам не понять.
func TestIsInternalTransfer(t *testing.T) {
	for _, c := range []struct {
		name string
		tx   domain.Transaction
		want bool
	}{
		{"расход-перевод", expenseTx(t, "Прочее", "Переводы", ""), true},
		{"расход-перевод, регистр и пробелы", expenseTx(t, " прочее ", "переводы", ""), true},
		{"обычный расход той же категории", expenseTx(t, "Прочее", "Подарки", ""), false},
		{"обычный расход", expenseTx(t, "Еда", "Продукты", ""), false},
		{"доход-перевод себе", incomeTx(t, "Перевод себе"), true},
		{"доход от мамы — настоящий", incomeTx(t, "Перевод от мамы"), false},
		{"зарплата", incomeTx(t, "Зарплата"), false},
	} {
		if got := c.tx.IsInternalTransfer(); got != c.want {
			t.Errorf("%s: IsInternalTransfer() = %v, ожидалось %v", c.name, got, c.want)
		}
	}
}

func expenseTx(t *testing.T, cat, sub, place string) domain.Transaction {
	t.Helper()
	m, err := domain.ParseMoney("2000")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: "01T", Kind: domain.KindExpense, Date: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		Amount: m, Category: cat, Subcategory: sub, Place: place, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	return tx
}

func incomeTx(t *testing.T, source string) domain.Transaction {
	t.Helper()
	m, err := domain.ParseMoney("2000")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: "01T", Kind: domain.KindIncome, Date: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		Amount: m, Source: source, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	return tx
}
