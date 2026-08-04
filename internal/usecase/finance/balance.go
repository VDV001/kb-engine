package finance

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// AccountBalance — что известно про остаток одного счёта.
//
// Два числа рядом, а не одно вместо другого. Confirmed — то, что владелец видел
// в приложении банка и подтвердил; Current — оно же минус траты, записанные
// после. Заменить первое вторым значило бы стереть единственный факт, который
// сверялся с реальностью.
type AccountBalance struct {
	Bank      string
	Confirmed domain.Money
	// Spent — сколько ушло с этого счёта после даты подтверждения.
	Spent   domain.Money
	Current domain.Money
	// Confirmed on which day. Пустая дата означает, что счёт не подтверждали
	// никогда, и тогда вычитать не от чего.
	ConfirmedOn string
	// NeedsConfirmation — расчёт ушёл в минус, то есть подтверждение устарело
	// настолько, что поступления, которых движок не видит, уже случились.
	// Число при этом не прячется: оно показывает, насколько далеко всё зашло.
	NeedsConfirmation bool
}

// CurrentBalances считает остаток каждого счёта на сейчас.
//
// Учитываются только расходы: домен не даёт доходу счёта, поэтому движок не
// знает, на какую карту пришли деньги. Значит расчёт может **занижать** остаток
// и никогда не завышает — и это ограничение названо на экране, а не спрятано.
//
// Траты в день подтверждения считаются: человек подтверждает баланс утром, а
// тратит днём, и день — самая мелкая единица, которой оперирует книга.
func CurrentBalances(accounts []domain.Account, txs []domain.Transaction) []AccountBalance {
	out := make([]AccountBalance, 0, len(accounts))
	for _, a := range accounts {
		b := AccountBalance{
			Bank:      a.Bank(),
			Confirmed: a.Balance(),
			Current:   a.Balance(),
		}
		if !a.Updated().IsZero() {
			b.ConfirmedOn = a.Updated().Format("2006-01-02")
			after := spentAfter(txs, a.Bank(), a.Updated())
			b.Spent = after
			b.Current = a.Balance().Sub(after)
			// Минус означает не долг, а слепоту расчёта: доходы счёта не имеют,
			// и на старом подтверждении траты неизбежно съедают остаток.
			b.NeedsConfirmation = b.Current.Kopecks() < 0
		}
		out = append(out, b)
	}
	return out
}

// spentAfter суммирует траты счёта, записанные не раньше дня подтверждения.
func spentAfter(txs []domain.Transaction, bank string, confirmed time.Time) domain.Money {
	var total domain.Money
	day := domain.Day(confirmed)
	for _, tx := range txs {
		if !tx.IsExpense() || tx.Account() != bank {
			continue
		}
		if tx.Date().Before(day) {
			continue
		}
		total = total.Add(tx.Amount())
	}
	return total
}
