package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func mustCurrency(t *testing.T, code string) domain.Currency {
	t.Helper()
	c, err := domain.NewCurrency(code)
	if err != nil {
		t.Fatalf("NewCurrency(%q): %v", code, err)
	}
	return c
}

func mustRate(t *testing.T, kopecks int64) domain.Rate {
	t.Helper()
	r, err := domain.NewRate(domain.NewMoney(kopecks))
	if err != nil {
		t.Fatalf("NewRate(%d): %v", kopecks, err)
	}
	return r
}

// Рублёвый счёт обязан работать без единой правки — ни в книге, ни у
// вызывающего (первый пункт приёмки #332). Прежний конструктор остаётся
// прежним, а не получает два новых аргумента: иначе «обратная совместимость»
// держалась бы на том, что каждое из существующих мест не забыло передать RUB.
func TestNewAccountStaysBaseCurrency(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	acc, err := domain.NewAccount("Сбербанк", domain.NewMoney(120000), now(), now)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	if !acc.Currency().IsBase() {
		t.Errorf("Currency() = %q, ожидался рубль", acc.Currency().Code())
	}
	// У рублёвого счёта курса нет и быть не должно: он и есть база, а не
	// оценка. Известный курс «1» здесь означал бы, что кто-то что-то измерял.
	if acc.Rate().Known() {
		t.Error("у рублёвого счёта известен курс — оценивать базу в базе нечем")
	}
	value, ok := acc.BaseValue()
	if !ok || value.Kopecks() != 120000 {
		t.Errorf("BaseValue() = %d, %v; ожидалось 120000 и true", value.Kopecks(), ok)
	}
}

// Валютный счёт хранит сумму В СВОЕЙ валюте и курс, по которому её оценили, а
// не одну пересчитанную цифру: сохранив только пересчёт, мы теряем ровно то,
// ради чего валюта заводилась.
func TestForeignAccountKeepsOwnAmountAndRate(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	usd := mustCurrency(t, "USD")
	rate := mustRate(t, 8428) // 84,28 ₽ за доллар

	acc, err := domain.NewForeignAccount("Наличные → Доллары", domain.NewMoney(50000), usd, rate, now(), now)
	if err != nil {
		t.Fatalf("NewForeignAccount: %v", err)
	}
	if acc.Currency().Code() != "USD" {
		t.Errorf("Currency() = %q, ожидалось USD", acc.Currency().Code())
	}
	// Баланс остаётся в своей валюте: 500,00 доллара, а не рубли.
	if acc.Balance().Kopecks() != 50000 {
		t.Errorf("Balance() = %d, ожидалось 50000 (в долларах)", acc.Balance().Kopecks())
	}
	// 500,00 × 84,28 = 42 140,00 ₽
	value, ok := acc.BaseValue()
	if !ok {
		t.Fatal("BaseValue() не смог оценить счёт с известным курсом")
	}
	if value.Kopecks() != 4214000 {
		t.Errorf("BaseValue() = %d, ожидалось 4214000 копеек", value.Kopecks())
	}
}

// «Курс неизвестен» — отдельный ответ, а не ноль и не последний известный.
// Витрина обязана сказать «оценка неизвестна», а не показать ноль: молча
// подставленный курс даёт число, на которое смотрят как на замер.
func TestForeignAccountWithoutRateRefusesToValue(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	try := mustCurrency(t, "TRY")

	acc, err := domain.NewForeignAccount("Наличные → Лиры", domain.NewMoney(300000), try, domain.UnknownRate(), now(), now)
	if err != nil {
		t.Fatalf("NewForeignAccount: %v", err)
	}
	if _, ok := acc.BaseValue(); ok {
		t.Error("счёт без курса оценён — вызывающий примет выдуманное число за замер")
	}
	// Сумма при этом не потеряна: неизвестен курс, а не остаток.
	if acc.Balance().Kopecks() != 300000 {
		t.Errorf("Balance() = %d, ожидалось 300000", acc.Balance().Kopecks())
	}
}

// Курс у базовой валюты — противоречие, а не безобидная избыточность: он
// означал бы, что рубль оценивают в рублях по какому-то отличному от единицы
// числу, и итог по счетам стал бы зависеть от того, кто его туда вписал.
func TestBaseCurrencyRejectsRate(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	rub := mustCurrency(t, "RUB")

	_, err := domain.NewForeignAccount("Сбербанк", domain.NewMoney(120000), rub, mustRate(t, 8428), now(), now)
	if !errors.Is(err, domain.ErrInvalidAccount) {
		t.Errorf("рублёвый счёт с курсом принят, ожидался ErrInvalidAccount (получено %v)", err)
	}
}
