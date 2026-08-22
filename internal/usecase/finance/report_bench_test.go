package finance_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Сводка финансов собирается на каждый запрос вкладки и на каждую отрисовку
// экрана денег в терминале. Числа выдуманы: суммы владельца в репозиторий
// не попадают ни при каких обстоятельствах.
func benchRecords(b *testing.B, n int) []finance.Record {
	b.Helper()
	clock := func() time.Time { return time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC) }
	cats := []string{"Продукты", "Транспорт", "Подписки", "Кафе", "Прочее"}
	accs := []string{"Альфа-Банк", "Сбербанк", "Т-Банк"}
	out := make([]finance.Record, 0, n)
	for i := range n {
		kind := domain.KindExpense
		cat, acc := cats[i%len(cats)], accs[i%len(accs)]
		if i%9 == 0 {
			kind, cat, acc = domain.KindIncome, "", ""
		}
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID:       fmt.Sprintf("bench-%06d", i),
			Kind:     kind,
			Date:     time.Date(2026, time.Month(1+i%7), 1+i%28, 0, 0, 0, 0, time.UTC),
			Amount:   domain.NewMoney(int64(100+i%9000) * 100),
			Category: cat,
			Account:  acc,
			Now:      clock,
		})
		if err != nil {
			b.Fatal(err)
		}
		rec, err := finance.NewRecord(tx, 1, clock())
		if err != nil {
			b.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

func BenchmarkSummarize(b *testing.B) {
	for _, n := range []int{100, 750} {
		recs := benchRecords(b, n)
		b.Run(fmt.Sprintf("записей_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = finance.Summarize(recs)
			}
		})
	}
}

// Фильтр по периоду стоит перед сводкой на пути вкладки финансов.
func BenchmarkMatch(b *testing.B) {
	recs := benchRecords(b, 750)
	f := finance.Filter{Year: 2026, Month: 7}
	b.ReportAllocs()
	for b.Loop() {
		_ = finance.Match(recs, f)
	}
}
