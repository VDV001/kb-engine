package finance_test

import (
	"reflect"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Деньги в разных валютах не складываются друг с другом и не пересчитываются
// по курсу (решение владельца 05.11): итог — по каждой валюте отдельно, и по
// роду счета отдельно, как на экране, чтобы долг не вычитался из денег на
// карте. Курс у счета в итог не входит вовсе.
func TestBalancesByCurrency(t *testing.T) {
	bal := func(bank, currency, confirmed, current string) finance.AccountBalance {
		return finance.AccountBalance{Bank: bank, Currency: currency, Confirmed: mustMoney(t, confirmed), Current: mustMoney(t, current),
			// Оценка по курсу нарочно неправдоподобная: попади она в итог — тест это увидит.
			BaseValue: mustMoney(t, "999999")}
	}
	cases := []struct {
		name string
		in   []finance.AccountBalance
		want []string // род · валюта · подтверждено · сейчас
	}{
		{"rubles and dollars stay apart",
			[]finance.AccountBalance{bal("Сбербанк", "RUB", "1000", "900"), bal("Wise", "USD", "10", "8"), bal("Т-Банк", "RUB", "500", "500")},
			[]string{" · RUB · 1500.00 · 1400.00", " · USD · 10.00 · 8.00"}},
		{"a debt is its own kind",
			[]finance.AccountBalance{bal("Сбербанк", "RUB", "1000", "1000"), bal("Долг → Сосед", "RUB", "-300", "-300"), bal("Резерв → Наличные", "USD", "50", "50")},
			[]string{" · RUB · 1000.00 · 1000.00", "Долг · RUB · -300.00 · -300.00", "Резерв · USD · 50.00 · 50.00"}},
		{"a hand-built balance without a currency counts as the book's",
			[]finance.AccountBalance{bal("Сбербанк", "", "1", "1"), bal("Т-Банк", "RUB", "2", "2")},
			[]string{" · RUB · 3.00 · 3.00"}},
		{"no accounts", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, tot := range finance.BalancesByCurrency(c.in) {
				got = append(got, tot.Group+" · "+tot.Currency+" · "+tot.Confirmed.String()+" · "+tot.Current.String())
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q\nwant %q", got, c.want)
			}
		})
	}
}

// Главная строка экрана — деньги по каждой валюте, без пересчета: все на
// счетах и свободные, тем же правилом рода и знака, что FreeMoney (отложенное
// и одолженное не свободно, обязательство вычитается).
func TestBalancesPerCurrency(t *testing.T) {
	bal := func(bank, currency, current string) finance.AccountBalance {
		return finance.AccountBalance{Bank: bank, Currency: currency, Current: mustMoney(t, current), BaseValue: mustMoney(t, "999999")}
	}
	got := finance.BalancesPerCurrency([]finance.AccountBalance{
		bal("Сбербанк", "RUB", "1000"),
		bal("Wise", "USD", "100"),
		bal("Резерв → Вклад", "RUB", "5000"), // отложено: в итог входит, в свободные нет
		bal("Долг → Сосед", "RUB", "-300"),   // обязательство: вычитается и из свободных
		bal("Резерв → Наличные", "USD", "50"),
		bal("Т-Банк", "", "200"), // собрано руками — валюта книги
	})
	var lines []string
	for _, c := range got {
		lines = append(lines, c.Currency+" · "+c.Current.String()+" · "+c.Free.String())
	}
	want := []string{"RUB · 5900.00 · 900.00", "USD · 150.00 · 100.00"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("got %q\nwant %q", lines, want)
	}
}
