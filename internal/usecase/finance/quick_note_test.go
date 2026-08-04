package finance_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Владелец набрал «23р Юрент страховка Сбер». Движок разобрал сумму, место и
// счёт, а «страховка» назвал незнакомым словом и запись заблокировал — потому
// что заметки в быстром вводе не было вовсе.
//
// Заметка отделяется тире или двоеточием с пробелами вокруг. Пробелы важны:
// «Альфа-Банк» и «Т-Банк» несут дефис внутри имени, и делить по нему значило бы
// ломать половину словаря счетов.

func vocabForNote() finance.Vocabulary {
	return finance.Vocabulary{
		Accounts: map[string]string{finance.NormalizeWord("Сбер"): "Сбербанк"},
		Places: map[string]finance.PlaceRule{
			finance.NormalizeWord("Юрент"): {Category: "Транспорт", Subcategory: "Самокат", Place: "Юрент"},
		},
	}
}

func TestQuick_textAfterDashIsTheNote(t *testing.T) {
	got, err := finance.ParseQuick("23р Юрент Сбер - страховка", vocabForNote())
	if err != nil {
		t.Fatalf("ParseQuick: %v", err)
	}
	if got.Params.Description != "страховка" {
		t.Errorf("заметка = %q, ожидалось «страховка»", got.Params.Description)
	}
	if len(got.Unknown) != 0 {
		t.Errorf("слова заметки попали в незнакомые: %v", got.Unknown)
	}
	// Остальное разобрано как прежде.
	if got.Params.Place != "Юрент" || got.Params.Account != "Сбербанк" {
		t.Errorf("разбор сломался: %+v", got.Params)
	}
}

func TestQuick_colonAlsoStartsTheNote(t *testing.T) {
	got, err := finance.ParseQuick("23р Юрент Сбер : пакет минут", vocabForNote())
	if err != nil {
		t.Fatalf("ParseQuick: %v", err)
	}
	if got.Params.Description != "пакет минут" {
		t.Errorf("заметка = %q, ожидалось «пакет минут»", got.Params.Description)
	}
}

// Дефис внутри имени — часть имени, а не разделитель. Иначе «Альфа-Банк»
// распалось бы на «Альфа» и заметку «Банк».
func TestQuick_hyphenInsideANameIsNotASeparator(t *testing.T) {
	voc := finance.Vocabulary{
		Accounts: map[string]string{finance.NormalizeWord("Альфа-Банк"): "Альфа-Банк"},
	}
	got, err := finance.ParseQuick("500 Альфа-Банк", voc)
	if err != nil {
		t.Fatalf("ParseQuick: %v", err)
	}
	if got.Params.Account != "Альфа-Банк" {
		t.Errorf("счёт = %q, имя разрезали по дефису", got.Params.Account)
	}
	if got.Params.Description != "" {
		t.Errorf("часть имени ушла в заметку: %q", got.Params.Description)
	}
}

// Строка без разделителя ведёт себя как раньше: незнакомое слово названо и
// запись блокируется. Это не регресс, а прежнее правило — гадать вместо
// человека движок не должен.
func TestQuick_withoutSeparatorUnknownStaysUnknown(t *testing.T) {
	got, err := finance.ParseQuick("23р Юрент страховка Сбер", vocabForNote())
	if err != nil {
		t.Fatalf("ParseQuick: %v", err)
	}
	if len(got.Unknown) != 1 || got.Unknown[0] != "страховка" {
		t.Errorf("незнакомые = %v, ожидалось [страховка]", got.Unknown)
	}
}
