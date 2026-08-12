package finance

import (
	"github.com/daniil/kb-engine/internal/domain"
)

// MayUnderstate reports whether this expense will be subtracted from a balance
// that already included it.
//
// Расчётный остаток вычитает из подтверждённого числа траты, записанные после
// момента подтверждения. Для траты, ДАТИРОВАННОЙ днём подтверждения, это
// предположение «человек её ещё не видел», и оно неверно ровно в одном случае:
// операция прошла по банку до того, как он снял остаток, а записали её позже.
// Тогда она вычитается второй раз, и расчёт занижен на её сумму.
//
// Умолчание остаётся прежним — вычитать. Движок объявил, что расчёт может
// только занижать и никогда не завышает; не вычитая, он разрешил бы завышение,
// а число больше настоящего опаснее числа меньше. Поэтому здесь чинится не
// арифметика, а молчание: спорный случай называется в момент записи, и человек
// решает сам — подтвердить баланс заново или оставить.
//
// Ответ «нет» означает «спора нет», а не «всё точно»: у траты в журнале есть
// день, а не момент операции, и узнать его движку неоткуда.
func MayUnderstate(tx domain.Transaction, acc domain.Account, confs Confirmations) bool {
	if !tx.IsExpense() || !domain.SameAccountName(tx.Account(), acc.Bank()) {
		return false
	}
	if acc.Updated().IsZero() || !tx.Date().Equal(domain.Day(acc.Updated())) {
		return false
	}
	moment, known := confs.At(acc.Bank())
	if !known {
		return false
	}
	at, ok := domain.RecordedAt(tx.ID())
	return ok && at.After(moment)
}
