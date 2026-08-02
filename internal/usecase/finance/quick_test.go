package finance_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// vocabulary — словарь, каким его увидит движок: слово, набранное человеком, и
// то, во что оно превращается в файле.
func vocabulary() finance.Vocabulary {
	return finance.Vocabulary{
		Accounts: map[string]string{
			"сбер": "Сбербанк", "сбербанк": "Сбербанк",
			"альфа": "Альфа-Банк", "альфабанк": "Альфа-Банк",
			"тбанк": "Т-Банк", "тинькофф": "Т-Банк",
		},
		Places: map[string]finance.PlaceRule{
			"такси":  {Category: "Транспорт", Subcategory: "Такси"},
			"магнит": {Category: "Еда", Subcategory: "Продукты", Place: "Магнит"},
			"аптека": {Category: "Здоровье", Subcategory: "Аптека"},
		},
	}
}

// Строка, набранная так, как человек её произносит, должна разложиться по полям
// без единого Tab. Это и есть смысл быстрого ввода: три слова вместо семи полей.
func TestParseQuick(t *testing.T) {
	for _, c := range []struct {
		name, line                            string
		kopecks                               int64
		category, subcategory, place, account string
	}{
		{
			name: "как в чате", line: "418р такси сбер",
			kopecks: 41800, category: "Транспорт", subcategory: "Такси", account: "Сбербанк",
		},
		{
			name: "запятая и место", line: "565,44 магнит альфа",
			kopecks: 56544, category: "Еда", subcategory: "Продукты", place: "Магнит", account: "Альфа-Банк",
		},
		{
			name: "точка вместо запятой", line: "565.44 магнит альфа",
			kopecks: 56544, category: "Еда", subcategory: "Продукты", place: "Магнит", account: "Альфа-Банк",
		},
		{
			name: "регистр не важен", line: "418 ТАКСИ Сбер",
			kopecks: 41800, category: "Транспорт", subcategory: "Такси", account: "Сбербанк",
		},
		{
			name: "дефис в названии банка", line: "418 такси т-банк",
			kopecks: 41800, category: "Транспорт", subcategory: "Такси", account: "Т-Банк",
		},
		{
			name: "порядок слов свободный", line: "сбер такси 418",
			kopecks: 41800, category: "Транспорт", subcategory: "Такси", account: "Сбербанк",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := finance.ParseQuick(c.line, vocabulary())
			if err != nil {
				t.Fatalf("ParseQuick(%q): %v", c.line, err)
			}
			if len(got.Unknown) != 0 {
				t.Errorf("нераспознанные слова: %v", got.Unknown)
			}
			p := got.Params
			if p.Amount.Kopecks() != c.kopecks {
				t.Errorf("сумма = %d копеек, ожидалось %d", p.Amount.Kopecks(), c.kopecks)
			}
			for _, f := range []struct{ name, got, want string }{
				{"категория", p.Category, c.category},
				{"подкатегория", p.Subcategory, c.subcategory},
				{"место", p.Place, c.place},
				{"счёт", p.Account, c.account},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, ожидалось %q", f.name, f.got, f.want)
				}
			}
			if p.Kind != domain.KindExpense {
				t.Errorf("вид = %q, ожидался расход", p.Kind)
			}
		})
	}
}

// Незнакомое слово не угадывается. Движок называет его и отдаёт решение
// человеку — вероятностное может предлагать, но не принимать.
func TestParseQuick_namesWhatItDoesNotKnow(t *testing.T) {
	got, err := finance.ParseQuick("140 живика альфа", vocabulary())
	if err != nil {
		t.Fatalf("ParseQuick: %v", err)
	}
	if len(got.Unknown) != 1 || got.Unknown[0] != "живика" {
		t.Fatalf("нераспознанное = %v, ожидалось [живика]", got.Unknown)
	}
	// Всё, что распозналось, остаётся распознанным: человеку доуточнять одно
	// поле, а не набирать строку заново.
	if got.Params.Amount.Kopecks() != 14000 {
		t.Errorf("сумма = %d копеек, ожидалось 14000", got.Params.Amount.Kopecks())
	}
	if got.Params.Account != "Альфа-Банк" {
		t.Errorf("счёт = %q, ожидался Альфа-Банк", got.Params.Account)
	}
	if got.Params.Category != "" {
		t.Errorf("категория = %q, ожидалась пустая: её никто не называл", got.Params.Category)
	}
}

// Строка без числа — не трата. Отказ приходит сразу и говорит, чего не хватило.
func TestParseQuick_refusesLineWithoutAmount(t *testing.T) {
	for _, line := range []string{"такси сбер", "", "   ", "магнит"} {
		t.Run(line, func(t *testing.T) {
			if _, err := finance.ParseQuick(line, vocabulary()); err == nil {
				t.Errorf("строка %q принята, ожидался отказ", line)
			} else if !strings.Contains(err.Error(), "сумм") {
				t.Errorf("отказ не называет сумму: %v", err)
			}
		})
	}
}
