package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

func foreignAccount(t *testing.T, name string, kopecks int64, code string, ratePer int64, day string) domain.Account {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	cur, err := domain.NewCurrency(code)
	if err != nil {
		t.Fatalf("NewCurrency(%q): %v", code, err)
	}
	rate := domain.UnknownRate()
	if ratePer > 0 {
		if rate, err = domain.NewRate(domain.NewMoney(ratePer)); err != nil {
			t.Fatalf("NewRate: %v", err)
		}
	}
	updated, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}
	acc, err := domain.NewForeignAccount(name, domain.NewMoney(kopecks), cur, rate, updated, now)
	if err != nil {
		t.Fatalf("NewForeignAccount: %v", err)
	}
	return acc
}

func rubAccount(t *testing.T, name string, kopecks int64, day string) domain.Account {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	updated, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}
	acc, err := domain.NewAccount(name, domain.NewMoney(kopecks), updated, now)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return acc
}

// Итог по счетам складывал разные единицы и называл результат рублями. Пока
// валютный счёт один, ошибка равна расхождению курсов между покупкой и
// сегодня; с двумя валютами итог перестаёт значить что-либо конкретное
// (второй пункт приёмки #332).
//
// Здесь проверяется, что складываются ОЦЕНКИ, а не сырые числа из ячеек.
func TestTotalsByGroup_valuesForeignAccounts(t *testing.T) {
	accounts := []domain.Account{
		rubAccount(t, "Сбербанк", 100000, "2026-09-01"),                           // 1 000,00 ₽
		foreignAccount(t, "Тумбочка → Доллары", 50000, "USD", 8428, "2026-09-01"), // 500,00 × 84,28
	}
	balances := finance.CurrentBalances(accounts, nil, nil)
	totals := finance.TotalsByGroup(balances)

	byGroup := map[string]finance.GroupTotal{}
	for _, g := range totals {
		byGroup[g.Group] = g
	}
	// Рублёвый счёт лежит в пустом роде и не меняется.
	if got := byGroup[""].Total.Kopecks(); got != 100000 {
		t.Errorf("рублёвый род = %d, ожидалось 100000", got)
	}
	// 500,00 × 84,28 = 42 140,00 ₽, а не 500,00 «рублей» из ячейки.
	if got := byGroup["Тумбочка"].Total.Kopecks(); got != 4214000 {
		t.Errorf("валютный род = %d, ожидалось 4214000 (оценка, а не сырое число)", got)
	}
}

// Счёт, который нечем оценить, не входит в итог МОЛЧА: итог, умолчавший о нём,
// утверждает больше, чем знает. Имя такого счёта называется, чтобы человек
// видел, чего в сумме нет.
func TestTotalsByGroup_namesUnvaluedAccounts(t *testing.T) {
	accounts := []domain.Account{
		foreignAccount(t, "Тумбочка → Доллары", 50000, "USD", 8428, "2026-09-01"),
		foreignAccount(t, "Тумбочка → Лиры", 300000, "TRY", 0, "2026-09-01"), // курса нет
	}
	totals := finance.TotalsByGroup(finance.CurrentBalances(accounts, nil, nil))
	if len(totals) != 1 {
		t.Fatalf("родов %d, ожидался один", len(totals))
	}
	g := totals[0]
	if g.Total.Kopecks() != 4214000 {
		t.Errorf("итог = %d, ожидалось 4214000 — неоценённый счёт в сумму не входит", g.Total.Kopecks())
	}
	if len(g.Unvalued) != 1 || g.Unvalued[0] != "Тумбочка → Лиры" {
		t.Errorf("Unvalued = %v, ожидалось [Тумбочка → Лиры]", g.Unvalued)
	}
}

// Итог называет, ПО КАКОМУ курсу и НА КАКОЙ момент он сложен. Без этого он
// выглядит текущей оценкой, будучи ценой входа: сумма замирает по курсу
// покупки и не переоценивается.
func TestTotalsByGroup_namesRatesUsed(t *testing.T) {
	accounts := []domain.Account{
		foreignAccount(t, "Тумбочка → Доллары", 50000, "USD", 8428, "2026-08-15"),
	}
	totals := finance.TotalsByGroup(finance.CurrentBalances(accounts, nil, nil))
	g := totals[0]
	if len(g.Rates) != 1 {
		t.Fatalf("курсов названо %d, ожидался один", len(g.Rates))
	}
	r := g.Rates[0]
	if r.Currency != "USD" {
		t.Errorf("Currency = %q, ожидалось USD", r.Currency)
	}
	if r.PerUnit.Kopecks() != 8428 {
		t.Errorf("PerUnit = %d, ожидалось 8428", r.PerUnit.Kopecks())
	}
	if r.On != "2026-08-15" {
		t.Errorf("On = %q, ожидалось 2026-08-15 — момент, на который курс верен", r.On)
	}
}

// Рублёвые счета курсов не называют вовсе: строка «RUB по курсу 1» — шум,
// который приучает пролистывать этот раздел, а вместе с ним и настоящие курсы.
func TestTotalsByGroup_rubleTotalsStayQuiet(t *testing.T) {
	accounts := []domain.Account{
		rubAccount(t, "Сбербанк", 100000, "2026-09-01"),
		rubAccount(t, "Заморозка → Вклад", 200000, "2026-09-01"),
	}
	for _, g := range finance.TotalsByGroup(finance.CurrentBalances(accounts, nil, nil)) {
		if len(g.Rates) != 0 {
			t.Errorf("род %q назвал курсы %v, а все счета рублёвые", g.Group, g.Rates)
		}
		if len(g.Unvalued) != 0 {
			t.Errorf("род %q объявил неоценённые счета %v, а все счета рублёвые", g.Group, g.Unvalued)
		}
	}
}

// «Свободно» — то же место, что итог по родам, и та же ошибка: сложив валютный
// остаток как рубли, оно отвечает не на тот вопрос. Проверяется отдельно,
// потому что считает его другая функция, и зелёный TotalsByGroup о ней ничего
// не говорит.
func TestFreeMoney_valuesForeignAccounts(t *testing.T) {
	accounts := []domain.Account{
		rubAccount(t, "Сбербанк", 100000, "2026-09-01"),
		foreignAccount(t, "Тумбочка → Доллары", 50000, "USD", 8428, "2026-09-01"),
	}
	// Валютный счёт лежит в роде «Тумбочка», то есть в свободные деньги не
	// входит по прежнему правилу. Свободен только рублёвый.
	if got := finance.FreeMoney(finance.CurrentBalances(accounts, nil, nil)); got.Kopecks() != 100000 {
		t.Errorf("FreeMoney = %d, ожидалось 100000", got.Kopecks())
	}

	// А вот валютный счёт БЕЗ рода свободен, и войти он обязан оценкой.
	plain := []domain.Account{
		foreignAccount(t, "Кошелёк долларовый", 50000, "USD", 8428, "2026-09-01"),
	}
	if got := finance.FreeMoney(finance.CurrentBalances(plain, nil, nil)); got.Kopecks() != 4214000 {
		t.Errorf("FreeMoney = %d, ожидалось 4214000 (оценка, а не сырое число)", got.Kopecks())
	}
}

// Счёт, который нечем оценить, в «свободно» не попадает: показать его сырое
// число значило бы выдать лиры за рубли, а показать ноль — сказать, что денег
// нет. Не входит вовсе, а сколько таких счетов, говорит итог по родам.
func TestFreeMoney_skipsUnvaluedAccounts(t *testing.T) {
	accounts := []domain.Account{
		foreignAccount(t, "Кошелёк лировый", 300000, "TRY", 0, "2026-09-01"),
	}
	if got := finance.FreeMoney(finance.CurrentBalances(accounts, nil, nil)); got.Kopecks() != 0 {
		t.Errorf("FreeMoney = %d, ожидался 0 — неоценённый счёт не входит", got.Kopecks())
	}
}
