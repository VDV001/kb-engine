package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Запись, которая занизит расчётный остаток, названа в момент записи.
//
// Расчёт вычитает из подтверждённого числа траты, записанные после момента
// подтверждения. Для траты, ДАТИРОВАННОЙ днём подтверждения, это предположение
// «человек её не видел» — и оно неверно, когда трата прошла по банку раньше, а
// записали её позже. Тогда она вычитается второй раз.
//
// Умолчание при этом остаётся прежним: вычитать. Движок объявил, что расчёт
// может только занижать и никогда не завышает, и не вычитать значило бы
// разрешить завышение. Чинится не число, а молчание — человеку говорят, что
// произошло, и что подтверждение баланса это исправит.
func TestMayUnderstate_sameDayExpenseRecordedAfterTheConfirmation(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	acc := accountAt(t, "Сбербанк", "1000.00", "2026-08-11")
	confs := finance.Confirmations{"Сбербанк": time.Date(2026, 8, 11, 16, 30, 0, 0, time.Local)}
	// записана в 23:19 того же дня — то есть после подтверждения
	tx := expenseRecordedAt(t, "2026-08-11T18:19:00Z", "2026-08-11", "Сбербанк", "105.00")

	if !finance.MayUnderstate(tx, acc, confs) {
		t.Error("запись занизит расчёт, но об этом не сказано")
	}
}

// Трата, записанная ДО подтверждения, в подтверждённое число вошла законно, и
// расчёт её не вычитает. Предупреждать здесь — значит приучить не читать.
func TestMayUnderstate_silentWhenRecordedBeforeTheConfirmation(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	acc := accountAt(t, "Сбербанк", "1000.00", "2026-08-11")
	confs := finance.Confirmations{"Сбербанк": time.Date(2026, 8, 11, 16, 30, 0, 0, time.Local)}
	tx := expenseRecordedAt(t, "2026-08-11T06:52:00Z", "2026-08-11", "Сбербанк", "42.00")

	if finance.MayUnderstate(tx, acc, confs) {
		t.Error("сказано о занижении там, где его нет")
	}
}

// Трата другого дня спора не создаёт: раньше дня подтверждения — банк её
// посчитал, позже — вычитается заведомо верно.
func TestMayUnderstate_silentOnAnotherDay(t *testing.T) {
	acc := accountAt(t, "Сбербанк", "1000.00", "2026-08-11")
	confs := finance.Confirmations{"Сбербанк": time.Date(2026, 8, 11, 16, 30, 0, 0, time.Local)}

	for _, date := range []string{"2026-08-10", "2026-08-12"} {
		tx := expenseRecordedAt(t, "2026-08-12T10:00:00Z", date, "Сбербанк", "50.00")
		if finance.MayUnderstate(tx, acc, confs) {
			t.Errorf("трата за %s не создаёт спора, но о ней сказано", date)
		}
	}
}

// Момент подтверждения неизвестен — сказать нечего. Молчание здесь честнее
// предупреждения: движок не знает, был ли спор вообще.
func TestMayUnderstate_silentWhenTheMomentIsUnknown(t *testing.T) {
	acc := accountAt(t, "Сбербанк", "1000.00", "2026-08-11")
	tx := expenseRecordedAt(t, "2026-08-11T18:19:00Z", "2026-08-11", "Сбербанк", "105.00")

	if finance.MayUnderstate(tx, acc, nil) {
		t.Error("сказано о занижении при неизвестном моменте подтверждения")
	}
}

// Чужой счёт не при чём.
func TestMayUnderstate_silentOnAnotherAccount(t *testing.T) {
	acc := accountAt(t, "Альфа-Банк", "1000.00", "2026-08-11")
	confs := finance.Confirmations{"Альфа-Банк": time.Date(2026, 8, 11, 16, 30, 0, 0, time.Local)}
	tx := expenseRecordedAt(t, "2026-08-11T18:19:00Z", "2026-08-11", "Сбербанк", "105.00")

	if finance.MayUnderstate(tx, acc, confs) {
		t.Error("трата по Сбербанку названа занижающей остаток Альфы")
	}
}

// Доход счёта не имеет и в расчёт не входит вовсе.
func TestMayUnderstate_silentOnIncome(t *testing.T) {
	acc := accountAt(t, "Сбербанк", "1000.00", "2026-08-11")
	confs := finance.Confirmations{"Сбербанк": time.Date(2026, 8, 11, 16, 30, 0, 0, time.Local)}

	m, err := domain.ParseMoney("500.00")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	day, _ := time.Parse(time.DateOnly, "2026-08-11")
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindIncome, Date: day, Amount: m, Source: "Возврат долга",
	}, func() string { return "01A-выдуманный" }, func() time.Time { return day })
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if finance.MayUnderstate(rec.Transaction(), acc, confs) {
		t.Error("доход назван занижающим остаток")
	}
}
