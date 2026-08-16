package domain

// Как записывается перемещение денег между своими счетами: расход помечается
// категорией и подкатегорией, доход — источником.
//
// ⚠️ Подкатегория расхода — «Перевод СЕБЕ», а не «Переводы», и это не
// придирка к слову. Прежнее значение отвечало на вопрос «что», но не на вопрос
// «кому», и замер на живых данных показал цену: единственная запись с ним за
// полгода оказалась возвратом переплаты заказчику. Деньги ушли по-настоящему, а
// движок исключал их из расходов — то есть занижал траты месяца на эту сумму.
//
// У дохода признак был однозначен с самого начала: «Перевод себе» называет
// сторону. Расход теперь спрашивается о том же.
const (
	transferCategory    = "Прочее"
	transferSubcategory = "Перевод себе"
	transferSource      = "Перевод себе"
)

// IsInternalTransfer reports whether this row moves money between the owner's
// own accounts instead of into or out of the household.
//
// Such a row is neither an expense nor an income: the money did not arrive and
// did not leave, it was moved. Adding it to real spending says the person spent
// more than they did.
//
// The rule lives here rather than in whatever assembles a report, because both
// surfaces have to answer the same question the same way. It used to live in
// the dashboard's build script and nowhere else, and the two totals disagreed
// for months with no way to tell which was right.
func (t Transaction) IsInternalTransfer() bool {
	if t.IsExpense() {
		return eqField(t.category, transferCategory) && eqField(t.subcategory, transferSubcategory)
	}
	// Доход перемещения не несёт счёта, поэтому вторая сторона перевода движку
	// не видна — ограничение названо на экране остатков и здесь не лечится.
	return eqField(t.source, transferSource)
}

// eqField compares two field values the way a person reading them would.
//
// Через FoldName, а не своим сравнением: движок уже решает вопрос «одно ли это
// имя» — так он сводит «Т-Банк» и «тбанк», ключи словаря и счета на листе.
// Второе правило рядом с первым однажды разошлось бы с ним, и разошлось бы
// молча: «перевод  себе» с двумя пробелами перестал бы быть переводом, а
// человек этого в книге не увидит.
func eqField(a, b string) bool {
	return FoldName(a) == FoldName(b)
}
