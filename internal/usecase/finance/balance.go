package finance

import (
	"time"

	"github.com/oklog/ulid/v2"

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
	// Group — род счёта, прочитанный из его имени («Заморозка → Хранение» →
	// «Заморозка»), и пустой у обычного счёта. Разбирает имя домен, а не
	// витрина: разбирая его сама, каждая витрина однажды разберёт иначе.
	Group           string
	NameWithinGroup string
}

// GroupTotal — сколько денег в одном роде счетов.
//
// Пустая группа означает обычные счета: деньги, которыми человек располагает
// сейчас. Ради них экран и открывают, поэтому они идут первыми.
type GroupTotal struct {
	Group string
	Total domain.Money
	Count int
}

// TotalsByGroup складывает остатки по родам счетов.
//
// Одно число «итого» смешивает три разные вещи: деньги на карте, деньги,
// отложенные и намеренно недоступные, и деньги, которых сейчас нет, потому что
// их занял человек. Складывать их молча — значит отвечать не на тот вопрос,
// который задают, глядя на итог.
//
// Порядок — тот, в котором группы встретились в книге. Владелец сам решил, что
// за чем идёт на листе «Счета», и витрине незачем это переставлять.
func TotalsByGroup(balances []AccountBalance) []GroupTotal {
	var out []GroupTotal
	at := make(map[string]int, len(balances))
	for _, b := range balances {
		// Группа берётся из имени, а не из поля рядом: поле — копия для
		// отображения, и вызывающий, собравший AccountBalance сам, оставил бы
		// его пустым. Тогда всё сложилось бы в одну кучу, и куча выглядела бы
		// правдоподобно.
		group, _ := domain.SplitAccountName(b.Bank)
		i, seen := at[group]
		if !seen {
			out = append(out, GroupTotal{Group: group})
			i = len(out) - 1
			at[group] = i
		}
		out[i].Total = out[i].Total.Add(b.Current)
		out[i].Count++
	}
	return out
}

// CurrentBalances считает остаток каждого счёта на сейчас.
//
// Учитываются только расходы: домен не даёт доходу счёта, поэтому движок не
// знает, на какую карту пришли деньги. Значит расчёт может **занижать** остаток
// и никогда не завышает — и это ограничение названо на экране, а не спрятано.
//
// Траты в день подтверждения НЕ считаются, и это исправление, а не выбор из
// двух равных.
//
// Человек подтверждает баланс, глядя в приложение банка, — то есть увиденное
// число уже включает всё, что он потратил в этот день. Вычитая их снова, движок
// показывал остаток меньше настоящего: владелец назвал 167 056,89, экран дал
// 167 033,89, разница — ровно сегодняшняя трата, учтённая дважды.
//
// Цена решения названа честно: трата, записанная после подтверждения в тот же
// день, попадёт в расчёт лишь завтра. Это ошибка в одну сторону и на один день,
// тогда как двойной учёт занижал остаток каждый раз при подтверждении.
func CurrentBalances(accounts []domain.Account, txs []domain.Transaction) []AccountBalance {
	out := make([]AccountBalance, 0, len(accounts))
	for _, a := range accounts {
		b := AccountBalance{
			Bank:            a.Bank(),
			Confirmed:       a.Balance(),
			Current:         a.Balance(),
			Group:           a.Group(),
			NameWithinGroup: a.NameWithinGroup(),
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

// spentAfter суммирует траты счёта, не вошедшие в подтверждённое число.
//
// Критерий — момент, когда строка появилась в книге, а не дата траты. Защита
// «в день подтверждения не вычитаем» бережёт от двойного учёта только то, что
// уже было записано, когда человек смотрел в приложение банка. Трата,
// датированная задним числом и записанная позже, в увиденное число не входила
// — и на старом критерии не вычиталась НИКОГДА, а не «до завтра».
//
// Момент записи читается из id: движок выдаёт ULID, и время внутри него —
// единственный след появления строки. Когда id не ULID (строка заведена не
// движком), момент неизвестен, и судим по дате траты, как раньше: выдумать его
// хуже, чем признать неизвестным.
func spentAfter(txs []domain.Transaction, bank string, confirmed time.Time) domain.Money {
	var total domain.Money
	day := domain.Day(confirmed)
	// Конец дня подтверждения: всё, записанное позже, человек в приложении
	// видеть не мог, чем бы оно ни было датировано.
	cutoff := day.AddDate(0, 0, 1)
	for _, tx := range txs {
		if !tx.IsExpense() || tx.Account() != bank {
			continue
		}
		if at, ok := recordedAt(tx.ID()); ok {
			// Время в ULID хранится в UTC, а день подтверждения живёт в зоне
			// книги: без приведения ночные записи попадали бы не в свои сутки.
			if !at.In(day.Location()).Before(cutoff) {
				total = total.Add(tx.Amount())
			}
			continue
		}
		if !tx.Date().After(day) {
			continue
		}
		total = total.Add(tx.Amount())
	}
	return total
}

// recordedAt возвращает момент появления строки, если id это ULID.
func recordedAt(id string) (time.Time, bool) {
	u, err := ulid.ParseStrict(id)
	if err != nil {
		return time.Time{}, false
	}
	return ulid.Time(u.Time()), true
}
