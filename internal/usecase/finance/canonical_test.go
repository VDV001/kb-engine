package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Владелец записал трату через терминал и набрал категорию строчными. Движок
// принял её как есть — и в отчёте появились две строки, «Транспорт» на 236
// записей и «транспорт» на одну. Разбивка раздвоилась молча.
//
// То же измерение нашло в живых данных два расходящихся места: «Своя Компания»
// против «Своя компания», «Пятерочка» против «Пятёрочка».
//
// Отсюда правило: значение, отличающееся от уже записанного только регистром,
// «ё» или дефисом, — то же самое значение, и пишется оно тем написанием, что
// в базе уже есть. Подстановка называется вслух: молча исправлять ввод человека
// нельзя, он должен видеть, что записано не то, что он набрал.

func recWith(t *testing.T, id, category, place string) finance.Record {
	t.Helper()
	amount, err := domain.ParseMoney("100.00")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	at := func() time.Time { return time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC) }
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: at(), Amount: amount,
		Category: category, Place: place,
	}, func() string { return id }, at)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}

func existingSpellings(t *testing.T) []finance.Record {
	t.Helper()
	return []finance.Record{
		recWith(t, "01A", "Транспорт", "Пятёрочка"),
		recWith(t, "01B", "Транспорт", "Пятёрочка"),
		recWith(t, "01C", "Транспорт", "Пятерочка"),
		recWith(t, "01D", "Еда", "Магнит"),
	}
}

func TestCanonical_usesTheSpellingAlreadyInTheLedger(t *testing.T) {
	p := finance.AddParams{Category: "транспорт", Place: "пятерочка"}

	got, fixed := finance.Canonical(existingSpellings(t), p)

	if got.Category != "Транспорт" {
		t.Errorf("категория = %q, ожидалось «Транспорт»", got.Category)
	}
	// Побеждает самое частое написание, а не первое встреченное: «Пятёрочка»
	// стоит дважды, «Пятерочка» один раз.
	if got.Place != "Пятёрочка" {
		t.Errorf("место = %q, ожидалось «Пятёрочка»", got.Place)
	}
	if len(fixed) != 2 {
		t.Fatalf("подстановок %d, ожидалось 2: %+v", len(fixed), fixed)
	}
}

// Подстановка обязана быть названа: человек набрал одно, записано другое, и
// узнать об этом из отчёта через месяц — то же самое молчание.
func TestCanonical_namesWhatItReplaced(t *testing.T) {
	p := finance.AddParams{Category: "транспорт"}

	_, fixed := finance.Canonical(existingSpellings(t), p)

	if len(fixed) != 1 {
		t.Fatalf("подстановок %d, ожидалась 1", len(fixed))
	}
	if fixed[0].Typed != "транспорт" || fixed[0].Used != "Транспорт" {
		t.Errorf("подстановка описана неверно: %+v", fixed[0])
	}
	if fixed[0].Field == "" {
		t.Error("не сказано, какое поле подставлено")
	}
}

// Новая категория — законное намерение, а не опечатка: её ни с чем не сверить,
// и трогать её нельзя.
func TestCanonical_leavesAGenuinelyNewValueAlone(t *testing.T) {
	p := finance.AddParams{Category: "Образование", Place: "Coursera"}

	got, fixed := finance.Canonical(existingSpellings(t), p)

	if got.Category != "Образование" || got.Place != "Coursera" {
		t.Errorf("новое значение изменено: %+v", got)
	}
	if len(fixed) != 0 {
		t.Errorf("подстановки на новом значении: %+v", fixed)
	}
}

// Точное совпадение — не подстановка, и сообщать о нём не о чем.
func TestCanonical_saysNothingWhenSpellingMatches(t *testing.T) {
	p := finance.AddParams{Category: "Транспорт", Place: "Магнит"}

	_, fixed := finance.Canonical(existingSpellings(t), p)

	if len(fixed) != 0 {
		t.Errorf("подстановка на точном совпадении: %+v", fixed)
	}
}

// Счёт сверяется так же: «сбер» и «Сбербанк» — разные слова, а вот «сбербанк»
// и «Сбербанк» одно, и в леджер должно уйти написание из книги.
func TestCanonical_normalizesTheAccountToo(t *testing.T) {
	amount, err := domain.ParseMoney("100.00")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	at := func() time.Time { return time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC) }
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: at(), Amount: amount,
		Category: "Еда", Account: "Сбербанк",
	}, func() string { return "01E" }, at)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, fixed := finance.Canonical([]finance.Record{rec},
		finance.AddParams{Category: "Еда", Account: "сбербанк"})

	if got.Account != "Сбербанк" {
		t.Errorf("счёт = %q, ожидался «Сбербанк»", got.Account)
	}
	if len(fixed) != 1 {
		t.Errorf("подстановка счёта не названа: %+v", fixed)
	}
}

// Словарь — записанное решение владельца, и оно старше исторической частоты.
// Живой случай: в леджере «Пятерочка» стоит семь раз против одной «Пятёрочки»,
// но 02.08 владелец решил писать «Пятёрочка», и это решение лежит в словаре.
// Большинство из старых записей не должно его перебивать.
func TestCanonical_vocabularyBeatsFrequency(t *testing.T) {
	existing := []finance.Record{
		recWith(t, "01A", "Еда", "Пятерочка"),
		recWith(t, "01B", "Еда", "Пятерочка"),
		recWith(t, "01C", "Еда", "Пятёрочка"),
	}
	voc := finance.Vocabulary{
		Places: map[string]finance.PlaceRule{
			finance.NormalizeWord("Пятерочка"): {Place: "Пятёрочка"},
		},
	}

	got, fixed := finance.CanonicalWith(existing, voc, finance.AddParams{Place: "пятерочка"})

	if got.Place != "Пятёрочка" {
		t.Errorf("место = %q, а словарь владельца говорит «Пятёрочка»", got.Place)
	}
	if len(fixed) != 1 {
		t.Errorf("подстановка не названа: %+v", fixed)
	}
}
