package finance

import (
	"reflect"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Множество сравниваемых полей берётся ИЗ СТРУКТУРЫ entry, а не из списка,
// вписанного в тест.
//
// Повод — замер, а не осторожность (issue #229): внешний тест перечислял пять
// полей из девяти, и подкатегорию с источником можно было выключить из
// сравнения при полностью зелёном наборе. Цена молчания тут про деньги: две
// траты одного дня, суммы, места и категории, различающиеся подкатегорией,
// схлопнулись бы в «повтор», и настоящая покупка была бы отвергнута; у дохода
// то же делает источник, который и есть его личность — счёта и категории домен
// доходу не даёт.
//
// Тест внутренний, потому что entry неэкспортирована. Проверка стоит на
// matches — общей развилке обоих путей (Duplicate и RepeatOf), поэтому
// выключенное поле не спрячется за сменой пути.
func TestEntryMatches_comparesEveryFieldOfEntry(t *testing.T) {
	day := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	amount, err := domain.ParseMoney("140")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	other, err := domain.ParseMoney("141")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}

	base := entry{
		kind: domain.KindExpense, date: day, amount: amount,
		category: "Здоровье", sub: "Аптека", place: "Живика",
		note: "лекарство", source: "Чек", account: "Альфа-Банк",
	}
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID: "01BX5ZZKBKACTAV9WEVGEMMVRZ", Kind: base.kind, Date: base.date, Amount: base.amount,
		Category: base.category, Subcategory: base.sub, Place: base.place,
		Description: base.note, Source: base.source, Account: base.account,
		Now: func() time.Time { return day.AddDate(0, 0, 1) },
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if !base.matches(tx) {
		t.Fatalf("образец не совпал сам с собой — фикстура собрана неверно")
	}

	// По одному различию на каждое поле entry. Ключ — имя поля структуры, и
	// именно по нему сверяется полнота: новое поле обязано появиться здесь,
	// иначе тест упадёт до того, как это поле окажется несравниваемым.
	differs := map[string]func(*entry){
		"kind":     func(e *entry) { e.kind = domain.KindIncome },
		"date":     func(e *entry) { e.date = day.AddDate(0, 0, -1) },
		"amount":   func(e *entry) { e.amount = other },
		"category": func(e *entry) { e.category = "Еда" },
		"sub":      func(e *entry) { e.sub = "Врачи" },
		"place":    func(e *entry) { e.place = "Монетка" },
		"note":     func(e *entry) { e.note = "другое" },
		"source":   func(e *entry) { e.source = "Скрин банка" },
		"account":  func(e *entry) { e.account = "Сбербанк" },
	}

	typ := reflect.TypeFor[entry]()
	for field := range typ.Fields() {
		name := field.Name
		mutate, ok := differs[name]
		if !ok {
			t.Fatalf("поле %s появилось в entry, но случая для него нет — "+
				"допишите различие, иначе поле можно выключить из сравнения молча", name)
		}
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if candidate.matches(tx) {
				t.Errorf("поле %s не сравнивается: запись с другим значением принята за ту же", name)
			}
		})
	}
}
