package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Правка обходила единственную проверку на повтор: добавить одинаковую трату
// движок не даёт, а сделать её одинаковой правкой — даёт. Дубль, попавший в
// журнал этим путём, через неделю неразрешим: никто не скажет, какая из двух
// строк была настоящей покупкой.
func TestRepeatOfRecord(t *testing.T) {
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	money := func(t *testing.T, rub float64) domain.Money {
		t.Helper()
		m, err := domain.MoneyFromFloat(rub)
		if err != nil {
			t.Fatalf("MoneyFromFloat: %v", err)
		}
		return m
	}
	mk := func(id string, rub float64, note string) finance.Record {
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: id, Kind: "expense", Date: day,
			Amount:   money(t, rub),
			Category: "Транспорт", Place: "Юрент", Account: "Сбербанк",
			Description: note,
		})
		if err != nil {
			t.Fatalf("NewTransaction: %v", err)
		}
		rec, err := finance.NewRecord(tx, 1, day)
		if err != nil {
			t.Fatalf("NewRecord: %v", err)
		}
		return rec
	}

	a := mk("01AAA", 200, "")
	b := mk("01BBB", 100, "")

	t.Run("правка, повторяющая другую запись, названа", func(t *testing.T) {
		edited := mk("01BBB", 200, "") // b стал таким же, как a
		got := finance.RepeatOf([]finance.Record{a, b}, edited)
		if got == nil {
			t.Fatal("повтор не найден")
		}
		if got.Transaction().ID() != "01AAA" {
			t.Errorf("названа запись %s, ожидалась 01AAA", got.Transaction().ID())
		}
	})

	// Запись всегда совпадает сама с собой: без пропуска по id любая правка
	// одного поля объявлялась бы повтором самой себя, и править стало бы нельзя
	// вовсе.
	t.Run("сама себе не повтор", func(t *testing.T) {
		if got := finance.RepeatOf([]finance.Record{a, b}, a); got != nil {
			t.Errorf("запись объявлена повтором самой себя: %s", got.Transaction().ID())
		}
	})

	// Две одинаковые траты в один день — обычная жизнь, а не ошибка: два пакета
	// минут на самокате. Различает их заметка, и разная заметка означает две
	// покупки.
	t.Run("разная заметка — две покупки", func(t *testing.T) {
		edited := mk("01BBB", 200, "второй пакет")
		if got := finance.RepeatOf([]finance.Record{a, b}, edited); got != nil {
			t.Errorf("записи с разными заметками сочтены повтором: %s", got.Transaction().ID())
		}
	})

	t.Run("регистр и пробелы различием не считаются", func(t *testing.T) {
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: "01BBB", Kind: "expense", Date: day,
			Amount:   money(t, 200),
			Category: " транспорт", Place: "ЮРЕНТ ", Account: "сбербанк",
		})
		if err != nil {
			t.Fatalf("NewTransaction: %v", err)
		}
		edited, err := finance.NewRecord(tx, 2, day)
		if err != nil {
			t.Fatalf("NewRecord: %v", err)
		}
		if got := finance.RepeatOf([]finance.Record{a, b}, edited); got == nil {
			t.Error("«сбербанк» и «Сбербанк» сочтены разными счетами")
		}
	})

	// Правило «это одна и та же трата» обязано быть одним на движок: путь
	// добавления и путь правки, разойдясь, начнут отвечать по-разному на один
	// вопрос — и обход найдётся сменой экрана, как это уже было с записью мимо
	// движка.
	t.Run("отвечает так же, как проверка на добавлении", func(t *testing.T) {
		p := finance.AddParams{
			Kind: "expense", Date: day, Amount: money(t, 200),
			Category: "Транспорт", Place: "Юрент", Account: "Сбербанк",
		}
		byAdd := finance.Duplicate([]finance.Record{a}, p)
		byEdit := finance.RepeatOf([]finance.Record{a}, mk("01CCC", 200, ""))
		if (byAdd == nil) != (byEdit == nil) {
			t.Fatalf("пути разошлись: add=%v edit=%v", byAdd != nil, byEdit != nil)
		}
	})
}
