package domain_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Два написания одного счёта — это один счёт, и решать это должен домен.
//
// Правило уже жило в движке — быстрый ввод сводит «Т-Банк», «т банк» и «тбанк»
// к одному слову, — но жило в usecase, куда лист «Счета» не смотрит. Пока оно
// там, заведение нового счёта сравнивает имена побуквенно, и «долг → кузнецов»
// рядом с «Займ → Коллеге» станут двумя строками словаря, решающего, что вообще
// считается счётом.
func TestSameAccountName(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"регистр не различает", "Сбербанк", "сбербанк", true},
		{"дефис и пробел не различают", "Т-Банк", "т банк", true},
		{"«ё» и «е» не различают", "Пятёрочка", "Пятерочка", true},
		{"пробелы по краям не различают", "  Альфа-Банк  ", "Альфа-Банк", true},
		{"пробелы вокруг стрелки не различают", "Займ → Коллеге", "Займ→Коллеге", true},
		{"разные счета остаются разными", "Займ → Коллеге", "Займ → Соседу", false},
		{"пустое имя не равно счёту", "", "Сбербанк", false},
		{"два пустых имени не равны", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.SameAccountName(tc.a, tc.b); got != tc.want {
				t.Errorf("SameAccountName(%q, %q) = %v, ожидалось %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// Стрелка в имени счёта — уже существующее соглашение книги: «Резерв →
// Хранение» и «Резерв → Депозит» читаются человеком как две части одного, и
// ровно поэтому долги пишутся так же.
//
// Группа нужна витринам: 150 000 на заморозке и 3 000 долга — не те же деньги,
// что лежат на карте, и итог, который их складывает молча, отвечает не на тот
// вопрос, который задают, глядя на него.
func TestAccountGroup(t *testing.T) {
	cases := []struct {
		bank      string
		group     string
		remainder string
	}{
		{"Займ → Коллеге", "Займ", "Коллеге"},
		{"Резерв → Наличные", "Резерв", "Наличные"},
		{"Сбербанк", "", "Сбербанк"},
		// Стрелка без пробелов — то же имя: пробелы вокруг неё оформление.
		{"Займ→Коллеге", "Займ", "Коллеге"},
		// Вторая стрелка не заводит третий уровень: группа — только первая часть.
		{"Займ → Коллеге → 2026", "Займ", "Коллеге → 2026"},
	}

	for _, tc := range cases {
		t.Run(tc.bank, func(t *testing.T) {
			acc := account(t, tc.bank)
			if got := acc.Group(); got != tc.group {
				t.Errorf("Group() = %q, ожидалось %q", got, tc.group)
			}
			if got := acc.NameWithinGroup(); got != tc.remainder {
				t.Errorf("NameWithinGroup() = %q, ожидалось %q", got, tc.remainder)
			}
		})
	}
}

func account(t *testing.T, bank string) domain.Account {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC) }
	acc, err := domain.NewAccount(bank, domain.NewMoney(0), now(), now)
	if err != nil {
		t.Fatalf("NewAccount(%q): %v", bank, err)
	}
	return acc
}
