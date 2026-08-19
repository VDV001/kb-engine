package finance_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// place строит расход в указанном месте — столько записей, сколько попросили.
func places(t *testing.T, spec map[string]int) []finance.Record {
	t.Helper()
	var out []finance.Record
	n := 0
	for name, count := range spec {
		for range count {
			n++
			out = append(out, expenseRecord(t, finance.AddParams{
				Kind: domain.KindExpense, Date: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
				Amount: money(t, "100"), Category: "Еда", Subcategory: "Продукты",
				Place: name, Account: "Сбербанк",
			}))
		}
	}
	return out
}

// formsOf собирает написания находки в строку для сравнения в тесте.
func formsOf(f finance.SpellingFinding) string {
	var parts []string
	for _, form := range f.Forms {
		parts = append(parts, form.Value)
	}
	return strings.Join(parts, "|")
}

func TestSpellingIssues(t *testing.T) {
	tests := []struct {
		name    string
		records []finance.Record
		voc     finance.Vocabulary
		want    string // ожидаемые написания через | , пусто — находок нет
		reason  string
	}{
		{
			// «Пятерочка» и «Пятёрочка» — одно место: домен считает их одним
			// именем, а в разбивке по местам они стоят двумя строками.
			name:    "ё против е",
			records: places(t, map[string]int{"Пятёрочка": 8, "Пятерочка": 1}),
			want:    "Пятёрочка|Пятерочка",
			reason:  "написание",
		},
		{
			name:    "регистр",
			records: places(t, map[string]int{"Своя Компания": 6, "своя компания": 2}),
			want:    "Своя Компания|своя компания",
			reason:  "написание",
		},
		{
			// Настоящий случай 17.08: 19 кириллических записей против одной
			// латинской. FoldName их не связывает — алфавит разный.
			name:    "латиница против кириллицы",
			records: places(t, map[string]int{"Бургер Кинг": 19, "Burger King": 1}),
			want:    "Бургер Кинг|Burger King",
			reason:  "алфавит",
		},
		{
			// Словарь — ПИСАТЕЛЬ: пока канон ведёт на форму, которой в журнале
			// нет, первая же покупка вернёт старое написание.
			name:    "канон словаря расходится с данными",
			records: places(t, map[string]int{"Италиан Пицца": 12}),
			voc: finance.Vocabulary{Places: map[string]finance.PlaceRule{
				"италиан": {Category: "Еда", Subcategory: "Фастфуд", Place: "Italian Pizza"},
			}},
			want:   "Italian Pizza|Италиан Пицца",
			reason: "словарь",
		},
		{
			name:    "чистые данные — находок нет",
			records: places(t, map[string]int{"Монетка": 5, "Кировский": 3}),
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finance.SpellingIssues(tt.records, tt.voc)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("находок %d, ждали ноль: %+v", len(got), got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("находок %d, ждали одну: %+v", len(got), got)
			}
			if forms := formsOf(got[0]); forms != tt.want {
				t.Errorf("написания = %q, ждали %q", forms, tt.want)
			}
			if !strings.Contains(got[0].Reason, tt.reason) {
				t.Errorf("причина = %q, ждали упоминание %q", got[0].Reason, tt.reason)
			}
		})
	}
}

// Число записей у каждой формы — то, чем человек решает, какая форма верная.
func TestSpellingIssues_countsEachForm(t *testing.T) {
	got := finance.SpellingIssues(places(t, map[string]int{"Жизнь Март": 37, "Жизньмарт": 4}), finance.Vocabulary{})
	if len(got) != 1 {
		t.Fatalf("находок %d, ждали одну", len(got))
	}
	counts := map[string]int{}
	for _, f := range got[0].Forms {
		counts[f.Value] = f.Count
	}
	if counts["Жизнь Март"] != 37 || counts["Жизньмарт"] != 4 {
		t.Errorf("счётчики = %+v, ждали 37 и 4", counts)
	}
	if got[0].Forms[0].Value != "Жизнь Март" {
		t.Errorf("первой идёт %q, ждали преобладающую форму", got[0].Forms[0].Value)
	}
}
