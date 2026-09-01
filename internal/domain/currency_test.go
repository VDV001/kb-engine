package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// У счёта не было валюты вовсе, поэтому любой счёт по конструкции рублёвый, а
// наличная валюта заводилась обычной строкой с суммой в рублях по курсу
// покупки. Имя говорило одно, число другое, и витрина не предупреждала ничем
// (issue #332).
//
// Нулевое значение Currency — рубль намеренно: старый код и старая книга не
// знают о валюте вовсе, и умолчание обязано быть тем, чем они пользовались.
// Особым случаем сделан не рубль, а всё остальное.
func TestCurrency(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		want    string
		wantErr bool
	}{
		{"трёхбуквенный код принимается", "USD", "USD", false},
		{"строчные приводятся к верхнему регистру", "try", "TRY", false},
		{"пробелы по краям не считаются", "  UZS  ", "UZS", false},
		{"рубль — обычный код", "RUB", "RUB", false},
		{"пустой код отвергается", "", "", true},
		{"два символа отвергаются", "US", "", true},
		{"четыре символа отвергаются", "USDT", "", true},
		{"цифры отвергаются", "US1", "", true},
		{"кириллица отвергается", "РУБ", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.NewCurrency(tc.code)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewCurrency(%q) = %v, ожидалась ошибка", tc.code, got.Code())
				}
				if !errors.Is(err, domain.ErrInvalidCurrency) {
					t.Errorf("ошибка %v не оборачивает ErrInvalidCurrency", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewCurrency(%q): %v", tc.code, err)
			}
			if got.Code() != tc.want {
				t.Errorf("Code() = %q, ожидалось %q", got.Code(), tc.want)
			}
		})
	}
}

// Нулевое значение обязано быть рублём, а не пустой строкой: на нём стоит вся
// обратная совместимость. Проверяется отдельно от таблицы, потому что это
// утверждение о ЗНАЧЕНИИ ПО УМОЛЧАНИЮ, а не о разборе кода.
func TestCurrencyZeroValueIsRUB(t *testing.T) {
	var zero domain.Currency
	if zero.Code() != "RUB" {
		t.Errorf("нулевая Currency.Code() = %q, ожидалось RUB", zero.Code())
	}
	if !zero.IsBase() {
		t.Error("нулевая Currency не считается базовой, а обязана")
	}
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatalf("NewCurrency: %v", err)
	}
	if usd.IsBase() {
		t.Error("USD объявлена базовой валютой, а базовая — рубль")
	}
	rub, err := domain.NewCurrency("RUB")
	if err != nil {
		t.Fatalf("NewCurrency: %v", err)
	}
	if !rub.IsBase() {
		t.Error("явно заданный RUB не считается базовым")
	}
	if rub != zero {
		t.Error("явный RUB и нулевое значение — разные Currency; тогда сравнение счетов начнёт врать")
	}
}

// «Курс неизвестен» — отдельный ответ, а не ноль и не последний известный.
// Молча подставленный курс даёт число, на которое смотрят как на замер
// (пункт приёмки #332).
func TestRate(t *testing.T) {
	unknown := domain.UnknownRate()
	if unknown.Known() {
		t.Error("UnknownRate() объявляет себя известным")
	}
	if _, ok := unknown.PerUnit(); ok {
		t.Error("неизвестный курс отдал значение — вызывающий примет его за замер")
	}

	rate, err := domain.NewRate(domain.NewMoney(8428)) // 84,28 ₽ за единицу
	if err != nil {
		t.Fatalf("NewRate: %v", err)
	}
	if !rate.Known() {
		t.Error("заданный курс объявляет себя неизвестным")
	}
	per, ok := rate.PerUnit()
	if !ok || per.Kopecks() != 8428 {
		t.Errorf("PerUnit() = %v, %v; ожидалось 8428 копеек и true", per.Kopecks(), ok)
	}

	// Ноль и отрицательный курс — не «неизвестно», а невозможное значение:
	// пропустив их, домен разрешил бы оценку в ноль рублей и молчаливое
	// обнуление итога по счетам.
	for _, bad := range []int64{0, -1, -8428} {
		if _, err := domain.NewRate(domain.NewMoney(bad)); !errors.Is(err, domain.ErrInvalidRate) {
			t.Errorf("NewRate(%d) прошёл, ожидался ErrInvalidRate (получено %v)", bad, err)
		}
	}
}
