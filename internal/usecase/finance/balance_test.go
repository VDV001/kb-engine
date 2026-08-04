package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Баланс на листе «Счета» — то, что владелец подтвердил глазами из приложения
// банка. Он не менялся от записи траты, и на витрине это выглядело так, будто
// деньги не тратились: записал 23 ₽ — итог «на счетах» прежний.
//
// Отсюда расчётный остаток: подтверждённое число минус траты, записанные ПОСЛЕ
// даты подтверждения. Оба показываются рядом — подтверждённое остаётся фактом,
// расчётное отвечает на «сколько сейчас».
//
// Доходы в расчёт не входят: домен не даёт доходу счёта, поэтому движок не
// знает, на какую карту пришли деньги. Значит расчёт может только занижать, и
// это названо вслух, а не спрятано.

func expenseOn(t *testing.T, id, account, date, amount string) domain.Transaction {
	t.Helper()
	m, err := domain.ParseMoney(amount)
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatalf("date: %v", err)
	}
	at := func() time.Time { return day }
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: day, Amount: m,
		Category: "Еда", Account: account,
	}, func() string { return id }, at)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec.Transaction()
}

func accountAt(t *testing.T, bank, amount, confirmed string) domain.Account {
	t.Helper()
	m, err := domain.ParseMoney(amount)
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	day, err := time.Parse(time.DateOnly, confirmed)
	if err != nil {
		t.Fatalf("date: %v", err)
	}
	acc, err := domain.NewAccount(bank, m, day, func() time.Time { return day })
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return acc
}

func TestCurrentBalance_subtractsExpensesAfterConfirmation(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{
		expenseOn(t, "01A", "Сбербанк", "2026-08-04", "23.00"),  // в день подтверждения
		expenseOn(t, "01B", "Сбербанк", "2026-08-03", "500.00"), // до — не считается
	}

	got := finance.CurrentBalances(accounts, recs)

	if len(got) != 1 {
		t.Fatalf("счетов %d, ожидался 1", len(got))
	}
	if got[0].Spent.String() != "23.00" {
		t.Errorf("списано после подтверждения = %s, ожидалось 23.00", got[0].Spent)
	}
	if got[0].Current.String() != "977.00" {
		t.Errorf("остаток = %s, ожидалось 977.00", got[0].Current)
	}
	// Подтверждённое число остаётся фактом и не подменяется.
	if got[0].Confirmed.String() != "1000.00" {
		t.Errorf("подтверждённое изменилось: %s", got[0].Confirmed)
	}
}

// Траты чужого счёта не должны уменьшать этот.
func TestCurrentBalance_countsOnlyItsOwnAccount(t *testing.T) {
	accounts := []domain.Account{
		accountAt(t, "Сбербанк", "1000.00", "2026-08-04"),
		accountAt(t, "Альфа-Банк", "500.00", "2026-08-04"),
	}
	recs := []domain.Transaction{expenseOn(t, "01A", "Альфа-Банк", "2026-08-05", "100.00")}

	got := finance.CurrentBalances(accounts, recs)

	byBank := map[string]finance.AccountBalance{}
	for _, b := range got {
		byBank[b.Bank] = b
	}
	if byBank["Сбербанк"].Current.String() != "1000.00" {
		t.Errorf("Сбербанк уменьшился от траты по Альфе: %s", byBank["Сбербанк"].Current)
	}
	if byBank["Альфа-Банк"].Current.String() != "400.00" {
		t.Errorf("Альфа = %s, ожидалось 400.00", byBank["Альфа-Банк"].Current)
	}
}

// Трата без счёта не принадлежит никому и не может уменьшать чей-то остаток.
func TestCurrentBalance_ignoresExpensesWithoutAccount(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{expenseOn(t, "01A", "", "2026-08-05", "70.00")}

	got := finance.CurrentBalances(accounts, recs)

	if got[0].Current.String() != "1000.00" {
		t.Errorf("трата без счёта списалась со Сбербанка: %s", got[0].Current)
	}
}

// Расчёт, ушедший в минус, — не факт, а признак того, что подтверждение
// устарело: доходы счёта не имеют, поэтому поступления в расчёт не входят, и
// на старом подтверждении траты неизбежно «съедают» остаток.
//
// Живой случай: Т-Банк подтверждён 20.05, трат после — на 597 ₽ при остатке 40,
// расчёт даёт −557. Показывать это как остаток значит врать; правильный ответ —
// сказать, что число просит подтверждения.
func TestCurrentBalance_marksTheResultAsStaleWhenItGoesNegative(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Т-Банк", "40.00", "2026-05-20")}
	recs := []domain.Transaction{expenseOn(t, "01A", "Т-Банк", "2026-06-01", "597.00")}

	got := finance.CurrentBalances(accounts, recs)

	if !got[0].NeedsConfirmation {
		t.Error("отрицательный расчёт не помечен как требующий подтверждения")
	}
	// Само число остаётся: прятать его — то же молчание, только с другой стороны.
	if got[0].Current.String() != "-557.00" {
		t.Errorf("расчёт = %s, ожидалось -557.00", got[0].Current)
	}
}

// Обычный счёт метки не получает: иначе она стоит на всех сразу и ничего не значит.
func TestCurrentBalance_doesNotMarkAHealthyAccount(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{expenseOn(t, "01A", "Сбербанк", "2026-08-04", "23.00")}

	if finance.CurrentBalances(accounts, recs)[0].NeedsConfirmation {
		t.Error("здоровый счёт помечен как требующий подтверждения")
	}
}
