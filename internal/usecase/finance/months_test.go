package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Дашборд разрешает выбрать НЕСКОЛЬКО месяцев сразу, а Year+Month описывают
// ровно один. Без набора месяцев фильтрация уехала бы в обработчик или во
// фронт, а вместе с ней и арифметика, которую мы как раз собираем в одном месте.
func TestMatchByMonths(t *testing.T) {
	recs := breakdownLedger(t)

	tests := []struct {
		name   string
		filter finance.Filter
		want   []string
	}{
		{"пустой набор не фильтрует", finance.Filter{Months: nil}, []string{"01A", "01B", "01C", "01D", "01E", "01F"}},
		{"один месяц", finance.Filter{Months: []string{"2026-01"}}, []string{"01B", "01C"}},
		{"несколько месяцев", finance.Filter{Months: []string{"2026-01", "2026-02"}}, []string{"01B", "01C", "01D", "01E", "01F"}},
		// Год в ключе значит именно год: январь 2025 и январь 2026 — разные месяцы.
		{"год различается", finance.Filter{Months: []string{"2025-01"}}, []string{"01A"}},
		{"порядок в наборе не важен", finance.Filter{Months: []string{"2026-02", "2025-01"}}, []string{"01A", "01D", "01E", "01F"}},
		{"несуществующий месяц даёт пусто, а не всё", finance.Filter{Months: []string{"2030-07"}}, nil},
		// Набор месяцев складывается с остальными полями, а не заменяет их.
		{"вместе с видом", finance.Filter{Months: []string{"2026-02"}, Kind: domain.KindExpense}, []string{"01D"}},
		{"вместе с категорией", finance.Filter{Months: []string{"2025-01", "2026-01"}, Category: "Еда"}, []string{"01A", "01B", "01C"}},
		// Мусор в наборе не должен пропускать всё: это фильтр, а не подсказка.
		{"пустая строка в наборе не совпадает ни с чем", finance.Filter{Months: []string{""}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finance.Match(recs, tt.filter)
			if len(got) != len(tt.want) {
				ids := make([]string, len(got))
				for i, r := range got {
					ids[i] = r.Transaction().ID()
				}
				t.Fatalf("matched %v (%d), want %v (%d)", ids, len(got), tt.want, len(tt.want))
			}
			for i := range tt.want {
				if id := got[i].Transaction().ID(); id != tt.want[i] {
					t.Errorf("record %d = %q, want %q", i, id, tt.want[i])
				}
			}
		})
	}
}

// Month и Months описывают одно и то же по-разному, поэтому проверяем, что
// прежнее поле продолжает работать: им пользуется CLI (`fin report --month`).
func TestMatchMonthStillWorksAlongsideMonths(t *testing.T) {
	recs := breakdownLedger(t)

	got := finance.Match(recs, finance.Filter{Year: 2026, Month: time.January})
	want := []string{"01B", "01C"}
	if len(got) != len(want) {
		t.Fatalf("matched %d, want %d", len(got), len(want))
	}
	for i := range want {
		if id := got[i].Transaction().ID(); id != want[i] {
			t.Errorf("record %d = %q, want %q", i, id, want[i])
		}
	}
}
