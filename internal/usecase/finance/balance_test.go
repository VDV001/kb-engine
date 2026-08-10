package finance_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

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
		expenseOn(t, "01A", "Сбербанк", "2026-08-05", "23.00"),  // после — считается
		expenseOn(t, "01B", "Сбербанк", "2026-08-04", "500.00"), // в день подтверждения — нет
		expenseOn(t, "01C", "Сбербанк", "2026-08-03", "100.00"), // до — нет
	}

	got := finance.CurrentBalances(accounts, recs, nil)

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

	got := finance.CurrentBalances(accounts, recs, nil)

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

	got := finance.CurrentBalances(accounts, recs, nil)

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

	got := finance.CurrentBalances(accounts, recs, nil)

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
	recs := []domain.Transaction{expenseOn(t, "01A", "Сбербанк", "2026-08-05", "23.00")}

	if finance.CurrentBalances(accounts, recs, nil)[0].NeedsConfirmation {
		t.Error("здоровый счёт помечен как требующий подтверждения")
	}
}

// expenseRecordedAt — трата, записанная в один момент, а датированная другим.
// Момент записи берётся из id: движок выдаёт ULID, и время внутри него —
// единственный след того, когда строка появилась в книге.
func expenseRecordedAt(t *testing.T, recorded, date, account, amount string) domain.Transaction {
	t.Helper()
	at, err := time.Parse(time.RFC3339, recorded)
	if err != nil {
		t.Fatalf("recorded: %v", err)
	}
	id := ulid.MustNew(ulid.Timestamp(at), ulid.Monotonic(rand.New(rand.NewSource(1)), 0)).String()
	return expenseOn(t, id, account, date, amount)
}

// Трата, датированная задним числом, но записанная ПОСЛЕ подтверждения, должна
// вычитаться — иначе она не будет учтена никогда.
//
// Случай живой и стоил владельцу расхождения: покупка 514,94 ₽ прошла 04.08,
// баланс подтверждён тем же днём, а строка появилась в книге только 05.08.
// Критерий стоял на дате траты, поэтому расчёт молча её пропускал: «в день
// подтверждения не вычитаем» защищает от двойного учёта только те траты,
// которые уже были записаны, когда человек смотрел в банк.
func TestCurrentBalance_countsAnExpenseRecordedAfterTheConfirmationDay(t *testing.T) {
	// Зона прибита, как в соседней проверке ниже: моменты записи заданы в UTC, а
	// границу дня подтверждения код берёт в зоне машины. Без этого тест верен
	// только там, где 12:20 UTC остаётся четвёртым числом, — в UTC+14 это уже
	// пятое, и запись «того же дня» законно становится записью следующего.
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{
		// задним числом, записана на следующий день — считается
		expenseRecordedAt(t, "2026-08-05T16:23:00Z", "2026-08-04", "Сбербанк", "514.94"),
		// записана в день подтверждения — уже в подтверждённом числе
		expenseRecordedAt(t, "2026-08-04T12:20:00Z", "2026-08-04", "Сбербанк", "23.00"),
		// обычная трата следующего дня — считается, как и раньше
		expenseRecordedAt(t, "2026-08-05T09:00:00Z", "2026-08-05", "Сбербанк", "100.00"),
	}

	got := finance.CurrentBalances(accounts, recs, nil)

	if got[0].Spent.String() != "614.94" {
		t.Errorf("списано = %s, ожидалось 614.94 (514.94 задним числом + 100.00)", got[0].Spent)
	}
	if got[0].Current.String() != "385.06" {
		t.Errorf("остаток = %s, ожидалось 385.06", got[0].Current)
	}
}

// id, из которого время не читается, оставляет старое поведение: судить по дате
// траты. Так ведут себя записи, заведённые не движком, и фикстуры старых тестов
// — выдумывать им момент записи хуже, чем признать, что он неизвестен.
func TestCurrentBalance_fallsBackToTheDateWhenTheIDCarriesNoTime(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{
		expenseOn(t, "не-ulid", "Сбербанк", "2026-08-04", "500.00"),
		expenseOn(t, "тоже-не-ulid", "Сбербанк", "2026-08-05", "23.00"),
	}

	got := finance.CurrentBalances(accounts, recs, nil)

	if got[0].Spent.String() != "23.00" {
		t.Errorf("списано = %s, ожидалось 23.00 — при нечитаемом id судим по дате траты", got[0].Spent)
	}
}

// Трата, ДАТИРОВАННАЯ раньше дня подтверждения, в подтверждённое число уже
// вошла: банк знает об операции с момента, когда она прошла, а не когда её
// записали в книгу. Момент записи для неё не решает ничего.
//
// Случай живой и стоил владельцу неверного числа на экране. Первый импорт выдал
// ULID сразу всей истории, поэтому «момент записи» у 580 строк из 603 — день
// импорта. Счёт, подтверждённый раньше этого дня, получал вычет всей своей
// доимпортной истории: так «Резерв → Депозит» показывал минус при
// подтверждении от 25.07, хотя единственная трата по нему датирована июнем.
func TestCurrentBalance_ignoresExpensesDatedBeforeTheConfirmation(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-07-25")}
	recs := []domain.Transaction{
		expenseRecordedAt(t, "2026-07-29T10:00:00Z", "2026-06-01", "Сбербанк", "700.00"),
		expenseRecordedAt(t, "2026-07-29T10:00:01Z", "2026-02-10", "Сбербанк", "200.00"),
	}

	got := finance.CurrentBalances(accounts, recs, nil)

	if got[0].Spent.String() != "0.00" {
		t.Errorf("списано = %s, ожидалось 0.00 — обе траты прошли до подтверждения и банк их уже посчитал", got[0].Spent)
	}
	if got[0].Current.String() != "1000.00" {
		t.Errorf("остаток = %s, ожидалось 1000.00", got[0].Current)
	}
}

// Трата, датированная ПОЗЖЕ дня подтверждения, вычитается всегда: в тот день
// её ещё не было, чем бы ни был её момент записи.
//
// Раньше здесь стояла дыра в пять часов: отсечка бралась полночью UTC, то есть
// 05:00 по книге, и запись, сделанная ночью, приписывалась предыдущим суткам.
func TestCurrentBalance_countsAnExpenseDatedAfterTheConfirmationEvenIfRecordedAtNight(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{
		// записана в 01:00 по книге (UTC+5), то есть уже 05.08 для человека
		expenseRecordedAt(t, "2026-08-04T20:00:00Z", "2026-08-05", "Сбербанк", "500.00"),
	}

	got := finance.CurrentBalances(accounts, recs, nil)

	if got[0].Spent.String() != "500.00" {
		t.Errorf("списано = %s, ожидалось 500.00 — трата произошла после дня подтверждения", got[0].Spent)
	}
}

// Написание счёта решает домен, а не побайтовое сравнение: лист «Счета» и
// строка в журнале могут расходиться регистром или пробелом вокруг стрелки.
// Расхождение стоило бы тихой потери денег — трата просто не нашла бы свой счёт.
func TestCurrentBalance_matchesTheAccountTheWayTheDomainDoes(t *testing.T) {
	accounts := []domain.Account{accountAt(t, "Т-Банк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{
		expenseRecordedAt(t, "2026-08-05T09:00:00Z", "2026-08-05", "т банк", "300.00"),
	}

	got := finance.CurrentBalances(accounts, recs, nil)

	if got[0].Spent.String() != "300.00" {
		t.Errorf("списано = %s, ожидалось 300.00 — «т банк» и «Т-Банк» это один счёт", got[0].Spent)
	}
}

// Трата ТОГО ЖЕ дня, записанная ночью следующего, вычитается: человек уже
// закрыл банковское приложение и лёг спать, в подтверждённое число она не
// вошла. Граница дня берётся в зоне машины — зоны книги в данных нет.
//
// Зона в тесте прибита намеренно: без этого проверка молчала бы на CI (UTC) и
// говорила бы только на машине владельца. Раньше здесь стояла полночь UTC,
// то есть 05:00 по книге, и такая запись не вычиталась никогда.
func TestCurrentBalance_countsASameDayExpenseRecordedAfterMidnightLocalTime(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-04")}
	recs := []domain.Transaction{
		// 01:00 ночи 05.08 по книге — то есть уже после дня подтверждения
		expenseRecordedAt(t, "2026-08-04T20:00:00Z", "2026-08-04", "Сбербанк", "400.00"),
		// 23:00 того же дня по книге — человек это ещё видел в банке
		expenseRecordedAt(t, "2026-08-04T18:00:00Z", "2026-08-04", "Сбербанк", "700.00"),
	}

	got := finance.CurrentBalances(accounts, recs, nil)

	if got[0].Spent.String() != "400.00" {
		t.Errorf("списано = %s, ожидалось 400.00 — вычитается только запись после полуночи по книге", got[0].Spent)
	}
}

// Известный МОМЕНТ подтверждения решает спор внутри дня, и это единственное, чем
// его можно решить.
//
// Замер 10.08.2026 на живых данных: баланс Сбера подтверждён днём, после него
// записана трата 470 ₽, и расчётный остаток разошёлся с банком ровно на неё.
// Три утренние траты того же дня в подтверждённое число вошли законно — их
// человек видел в приложении, когда снимал остаток.
//
// Пока у подтверждения был только ДЕНЬ, движок обязан был гадать и гадал всегда
// в одну сторону — «человек это видел». Ошибка при этом не рассасывалась к
// следующему дню: критерий стоит на дате подтверждения, поэтому такая трата
// оставалась невидимой до следующего подтверждения, то есть бессрочно.
func TestCurrentBalance_subtractsASameDayExpenseRecordedAfterTheConfirmationMoment(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-10")}
	recs := []domain.Transaction{
		// 11:52 по книге — записана до того, как владелец снял остаток
		expenseRecordedAt(t, "2026-08-10T06:52:00Z", "2026-08-10", "Сбербанк", "42.00"),
		// 15:13 по книге — записана после, в увиденное число не входила
		expenseRecordedAt(t, "2026-08-10T10:13:00Z", "2026-08-10", "Сбербанк", "470.00"),
	}
	// 12:00 по книге — момент, когда остаток был подтверждён
	confirmed := finance.Confirmations{"Сбербанк": time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)}

	got := finance.CurrentBalances(accounts, recs, confirmed)

	if got[0].Spent.String() != "470.00" {
		t.Errorf("списано = %s, ожидалось 470.00 — вычитается только запись после момента подтверждения", got[0].Spent)
	}
	if got[0].Current.String() != "530.00" {
		t.Errorf("остаток = %s, ожидалось 530.00", got[0].Current)
	}
}

// Счёт, о моменте подтверждения которого ничего не известно, сохраняет прежнее
// поведение целиком.
//
// Файл состояния появился позже книги, и у счетов, подтверждённых до него,
// момента нет и не будет. Выдумать его — значит вычесть трату, которую человек
// уже видел, то есть заменить завышение занижением; это не починка, а вторая
// ошибка вместо первой.
func TestCurrentBalance_keepsTheOldRuleWhenTheMomentIsUnknown(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	accounts := []domain.Account{accountAt(t, "Сбербанк", "1000.00", "2026-08-10")}
	recs := []domain.Transaction{
		expenseRecordedAt(t, "2026-08-10T10:13:00Z", "2026-08-10", "Сбербанк", "470.00"),
	}

	// Момент известен про ДРУГОЙ счёт: карта не пуста, но об этом сказать нечего.
	confirmed := finance.Confirmations{"Альфа-Банк": time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)}

	if got := finance.CurrentBalances(accounts, recs, confirmed); got[0].Spent.String() != "0.00" {
		t.Errorf("списано = %s, ожидалось 0.00 — без момента подтверждения правило прежнее", got[0].Spent)
	}
}

// Написание счёта в файле состояния решает домен, а не побайтовое равенство.
//
// Лист «Счета» и журнал расходятся регистром и пробелами вокруг стрелки, и это
// уже стоило движку тихо потерянных трат в spentAfter. Момент, не нашедший свой
// счёт, стоил бы того же: правило молча вернулось бы к прежнему.
func TestCurrentBalance_matchesTheConfirmationTheWayTheDomainDoes(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("книга", 5*60*60)
	t.Cleanup(func() { time.Local = saved })

	accounts := []domain.Account{accountAt(t, "Долг → Отец", "1000.00", "2026-08-10")}
	recs := []domain.Transaction{
		expenseRecordedAt(t, "2026-08-10T10:13:00Z", "2026-08-10", "долг→отец", "470.00"),
	}
	confirmed := finance.Confirmations{"долг → отец": time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)}

	if got := finance.CurrentBalances(accounts, recs, confirmed); got[0].Spent.String() != "470.00" {
		t.Errorf("списано = %s, ожидалось 470.00 — счёт сопоставляется правилом домена", got[0].Spent)
	}
}
