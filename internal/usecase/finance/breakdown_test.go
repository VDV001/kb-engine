package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// breakdownLedger — набор, в котором каждый разрез даёт разный ответ, чтобы
// перепутанные поля были видны: две подкатегории внутри одной категории, одно
// место, повторяющееся в разных категориях, источник и у расхода (чем платил),
// и у дохода (откуда пришло), два месяца и два года.
func breakdownLedger(t *testing.T) []finance.Record {
	t.Helper()
	build := func(id, kind string, y int, m time.Month, d int, amount int64, category, sub, place, source string) finance.Record {
		account := ""
		if kind == domain.KindExpense {
			account = "Сбербанк"
		}
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: id, Kind: kind,
			Date:        time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
			Amount:      domain.NewMoney(amount),
			Category:    category,
			Subcategory: sub,
			Place:       place,
			Source:      source,
			Account:     account,
			Now:         clock,
		})
		if err != nil {
			t.Fatalf("build transaction %s: %v", id, err)
		}
		rec, err := finance.NewRecord(tx, 1, importedAt)
		if err != nil {
			t.Fatalf("build record %s: %v", id, err)
		}
		return rec
	}
	return []finance.Record{
		// Январь 2025 — прошлый год, тот же месяц, что и январь 2026 ниже.
		build("01A", domain.KindExpense, 2025, time.January, 15, 50000, "Еда", "Кафе", "Пекарня", "Тинькофф"),
		// Январь 2026.
		build("01B", domain.KindExpense, 2026, time.January, 10, 30000, "Еда", "Кафе", "Пекарня", "Тинькофф"),
		build("01C", domain.KindExpense, 2026, time.January, 10, 20000, "Еда", "Продукты", "Пятёрочка", "Сбербанк"),
		// Февраль 2026 — то же место, но другая категория.
		build("01D", domain.KindExpense, 2026, time.February, 3, 70000, "Развлечения", "", "Пекарня", "Сбербанк"),
		// Доходы: источник дохода не должен смешаться с источником оплаты.
		build("01E", domain.KindIncome, 2026, time.February, 5, 9000000, "", "", "", "Зарплата"),
		build("01F", domain.KindIncome, 2026, time.February, 20, 300000, "", "", "", "Стипендия"),
	}
}

func TestSummarizeBySubcategory(t *testing.T) {
	s := finance.Summarize(breakdownLedger(t))

	// Ожидание: только расходы с непустой подкатегорией, крупные первыми.
	// «Развлечения» без подкатегории в разрез не попадает вовсе — пустая
	// подкатегория это отсутствие данных, а не группа «прочее».
	want := []struct {
		category, sub string
		total         int64
		count         int
	}{
		{"Еда", "Кафе", 80000, 2},
		{"Еда", "Продукты", 20000, 1},
	}
	if len(s.BySubcategory) != len(want) {
		t.Fatalf("BySubcategory: got %d rows %+v, want %d", len(s.BySubcategory), s.BySubcategory, len(want))
	}
	for i, w := range want {
		got := s.BySubcategory[i]
		if got.Category != w.category || got.Subcategory != w.sub {
			t.Errorf("row %d: got %q → %q, want %q → %q", i, got.Category, got.Subcategory, w.category, w.sub)
		}
		if got.Total.Kopecks() != w.total {
			t.Errorf("row %d (%s → %s): total %d, want %d", i, w.category, w.sub, got.Total.Kopecks(), w.total)
		}
		if got.Count != w.count {
			t.Errorf("row %d (%s → %s): count %d, want %d", i, w.category, w.sub, got.Count, w.count)
		}
	}
}

func TestSummarizeByPlaceAndSource(t *testing.T) {
	s := finance.Summarize(breakdownLedger(t))

	tests := []struct {
		name string
		got  []finance.CategoryTotal
		want []struct {
			name  string
			total int64
			count int
		}
	}{
		{
			// Место складывается по всем категориям: «Пекарня» встречается и в
			// «Еде», и в «Развлечениях», и это одно место.
			name: "ByPlace",
			got:  s.ByPlace,
			want: []struct {
				name  string
				total int64
				count int
			}{
				{"Пекарня", 150000, 3},
				{"Пятёрочка", 20000, 1},
			},
		},
		{
			// Источник у расхода — чем платил.
			name: "BySource",
			got:  s.BySource,
			want: []struct {
				name  string
				total int64
				count int
			}{
				{"Сбербанк", 90000, 2},
				{"Тинькофф", 80000, 2},
			},
		},
		{
			// Источник у дохода — откуда пришло. Не должен смешаться с BySource.
			name: "IncomeBySource",
			got:  s.IncomeBySource,
			want: []struct {
				name  string
				total int64
				count int
			}{
				{"Зарплата", 9000000, 1},
				{"Стипендия", 300000, 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.got) != len(tc.want) {
				t.Fatalf("got %d rows %+v, want %d", len(tc.got), tc.got, len(tc.want))
			}
			for i, w := range tc.want {
				if tc.got[i].Category != w.name {
					t.Errorf("row %d: name %q, want %q", i, tc.got[i].Category, w.name)
				}
				if tc.got[i].Total.Kopecks() != w.total {
					t.Errorf("row %d (%s): total %d, want %d", i, w.name, tc.got[i].Total.Kopecks(), w.total)
				}
				if tc.got[i].Count != w.count {
					t.Errorf("row %d (%s): count %d, want %d", i, w.name, tc.got[i].Count, w.count)
				}
			}
		})
	}
}

func TestSummarizeByMonthKeepsYears(t *testing.T) {
	s := finance.Summarize(breakdownLedger(t))

	// Ключ включает год. Дашборд на Python складывал январь 2025 и январь 2026
	// в один столбец (индекс по номеру месяца), и за четыре года истории это
	// молча смешивает разные годы — здесь такого не происходит.
	want := []struct {
		month string
		total int64
		count int
	}{
		{"2025-01", 50000, 1},
		{"2026-01", 50000, 2},
		{"2026-02", 70000, 1},
	}
	if len(s.ByMonth) != len(want) {
		t.Fatalf("ByMonth: got %d rows %+v, want %d", len(s.ByMonth), s.ByMonth, len(want))
	}
	for i, w := range want {
		got := s.ByMonth[i]
		if got.Month != w.month {
			t.Errorf("row %d: month %q, want %q (порядок должен быть хронологическим)", i, got.Month, w.month)
		}
		if got.Total.Kopecks() != w.total {
			t.Errorf("row %d (%s): total %d, want %d", i, w.month, got.Total.Kopecks(), w.total)
		}
		if got.Count != w.count {
			t.Errorf("row %d (%s): count %d, want %d", i, w.month, got.Count, w.count)
		}
	}
}

func TestSummarizeByDay(t *testing.T) {
	s := finance.Summarize(breakdownLedger(t))

	// Только дни с расходами, хронологически. Два расхода 2026-01-10 сливаются
	// в одну строку — график плотности рисует день, а не транзакцию.
	want := []struct {
		date  string
		total int64
		count int
	}{
		{"2025-01-15", 50000, 1},
		{"2026-01-10", 50000, 2},
		{"2026-02-03", 70000, 1},
	}
	if len(s.ByDay) != len(want) {
		t.Fatalf("ByDay: got %d rows %+v, want %d", len(s.ByDay), s.ByDay, len(want))
	}
	for i, w := range want {
		got := s.ByDay[i]
		if got.Date != w.date {
			t.Errorf("row %d: date %q, want %q", i, got.Date, w.date)
		}
		if got.Total.Kopecks() != w.total {
			t.Errorf("row %d (%s): total %d, want %d", i, w.date, got.Total.Kopecks(), w.total)
		}
		if got.Count != w.count {
			t.Errorf("row %d (%s): count %d, want %d", i, w.date, got.Count, w.count)
		}
	}
}

func TestSummarizeBreakdownsIgnoreIncomeForExpenseCuts(t *testing.T) {
	// Доход не имеет ни категории, ни места, ни подкатегории — но у него ЕСТЬ
	// источник, и именно на нём легко перепутать разрезы. Проверяем, что
	// расходные разрезы дохода не видят вовсе.
	s := finance.Summarize(breakdownLedger(t))

	for _, row := range s.BySource {
		if row.Category == "Зарплата" || row.Category == "Стипендия" {
			t.Errorf("BySource содержит источник дохода %q — разрезы смешались", row.Category)
		}
	}
	for _, row := range s.IncomeBySource {
		if row.Category == "Сбербанк" || row.Category == "Тинькофф" {
			t.Errorf("IncomeBySource содержит источник оплаты %q — разрезы смешались", row.Category)
		}
	}
	// Доходы не участвуют в месячных и дневных суммах расходов: 2026-02 это
	// только расход 70000, хотя в этом месяце пришло 9 300 000 дохода.
	for _, row := range s.ByMonth {
		if row.Month == "2026-02" && row.Total.Kopecks() != 70000 {
			t.Errorf("ByMonth[2026-02] = %d, want 70000 — доход попал в расходы", row.Total.Kopecks())
		}
	}
}

func TestSummarizeEmptyLedgerHasNoBreakdownRows(t *testing.T) {
	s := finance.Summarize(nil)

	cuts := map[string]int{
		"BySubcategory":  len(s.BySubcategory),
		"ByPlace":        len(s.ByPlace),
		"BySource":       len(s.BySource),
		"IncomeBySource": len(s.IncomeBySource),
		"ByMonth":        len(s.ByMonth),
		"ByDay":          len(s.ByDay),
	}
	for name, n := range cuts {
		if n != 0 {
			t.Errorf("%s на пустом леджере даёт %d строк, want 0", name, n)
		}
	}
}
