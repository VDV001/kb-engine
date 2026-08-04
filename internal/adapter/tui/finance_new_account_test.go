package tui_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
)

// ctrlN — подтверждение «этого счёта ещё нет, заведи». Отдельная клавиша, а не
// повторный Enter: Enter в этой форме уже значит «записать», и нагружать его
// вторым смыслом значит сделать заведение счёта следствием нетерпеливости.
func ctrlN() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlN} }

var errAccountRefused = errors.New("счёт уже есть на листе «Счета»")

// namedAccount собирает один счёт с подтверждённым остатком.
func namedAccount(t *testing.T, bank, sum string) domain.Account {
	t.Helper()
	at := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC) }
	m, err := domain.ParseMoney(sum)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", sum, err)
	}
	acc, err := domain.NewAccount(bank, m, at, clock)
	if err != nil {
		t.Fatalf("NewAccount(%q): %v", bank, err)
	}
	return acc
}

// Завести счёт можно было только из CLI, и это отправляло владельца из
// терминала в терминал же — но другой командой и с путями, которые на экране
// уже открыты. Форма баланса знает и имя, и сумму: ей не хватало только
// отдельного намерения «этого счёта ещё нет».
//
// Намерение отдельное, потому что имя, которого лист не знает, — чаще опечатка,
// чем новый счёт. Молча заводя его, экран пополнял бы словарь, решающий, что
// вообще считается счётом, каждой промашкой в раскладке.
func TestNewAccount_offersToCreateWhenTheNameIsUnknown(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Долг → Отец")
	m = press(m, runes("3000"))
	m = press(m, enter())

	// Ничего не записано: ни как баланс существующего счёта, ни как новый.
	if len(acc.got) != 0 {
		t.Fatalf("записан баланс несуществующего счёта: %+v", acc.got)
	}
	if len(acc.created) != 0 {
		t.Fatalf("счёт заведён без подтверждения: %+v", acc.created)
	}
	// Форма осталась открытой с набранным: исправлять опечатку заново — цена,
	// которую платит человек за подсказку движка.
	if !m.OnBalanceForm() {
		t.Error("форма закрылась, набранное потеряно")
	}
	view := m.View()
	for _, want := range []string{"Долг → Отец", "ctrl+n"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(want)) {
			t.Errorf("экран не подсказал, как завести счёт (нет %q)\n--- view ---\n%s", want, view)
		}
	}
}

// Подтверждение заводит счёт с той же суммой, которая уже набрана: заставлять
// набирать её второй раз значит просить человека повторить то, что он только
// что сделал, и получить расхождение при опечатке.
func TestNewAccount_createsOnConfirmation(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Долг → Отец")
	m = press(m, runes("3000"))
	m = press(m, enter())
	m = press(m, ctrlN())

	if len(acc.created) != 1 {
		t.Fatalf("счёт заведён %d раз, ожидался 1: %+v", len(acc.created), acc.created)
	}
	if acc.created[0].bank != "Долг → Отец" {
		t.Errorf("имя = %q, ожидалось «Долг → Отец»", acc.created[0].bank)
	}
	if acc.created[0].amount.Kopecks() != 300000 {
		t.Errorf("сумма = %d копеек, ожидалось 300000", acc.created[0].amount.Kopecks())
	}
	if m.OnBalanceForm() {
		t.Error("форма осталась открытой после заведения счёта")
	}
	if view := m.View(); !strings.Contains(view, "новый счёт") {
		t.Errorf("экран не сказал, что счёт новый\n--- view ---\n%s", view)
	}
}

// Клавиша работает только там, где движок сам сказал «такого счёта нет».
// Иначе она стала бы вторым способом писать баланс — тем, который не проверяет
// написание и заводит «сбербанк» рядом со «Сбербанком».
func TestNewAccount_doesNothingWithoutTheOffer(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Сбербанк")
	m = press(m, runes("500"))
	press(m, ctrlN())

	if len(acc.created) != 0 {
		t.Fatalf("счёт заведён без предложения: %+v", acc.created)
	}
}

// Написание, которое лист уже знает, счётом не считается — даже когда буквы
// другие. Предложи экран завести «сбербанк», в книге появилось бы два счёта
// об одном, и оба выглядели бы одинаково правдоподобно.
func TestNewAccount_doesNotOfferForAKnownSpelling(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "сбербанк")
	m = press(m, runes("500"))
	press(m, enter())

	// Это обычная запись баланса: домен считает написания одним счётом.
	if len(acc.created) != 0 {
		t.Errorf("предложено завести счёт, который уже есть: %+v", acc.created)
	}
	if len(acc.got) != 1 {
		t.Fatalf("баланс записан %d раз, ожидался 1", len(acc.got))
	}
}

// Отказ движка на заведении показывается так же, как отказ на записи: набранное
// остаётся, причина названа. Отдельный путь для нового счёта не значит отдельных
// правил обращения с ошибкой.
func TestNewAccount_namesTheRefusal(t *testing.T) {
	acc := accountsStub(t)
	acc.createErr = errAccountRefused
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Долг → Отец")
	m = press(m, runes("3000"))
	m = press(m, enter())
	m = press(m, ctrlN())

	if !m.OnBalanceForm() {
		t.Fatal("форма закрылась после отказа")
	}
	if view := m.View(); !strings.Contains(view, errAccountRefused.Error()) {
		t.Errorf("причина отказа не показана\n--- view ---\n%s", view)
	}
}

// Экран финансов показывает рода счетов так же, как карточка в вебе: иначе одна
// поверхность отвечает «у тебя 154 000», а вторая — «свободно 1 000», и обе
// правы. Считает при этом один usecase, а не две реализации одной арифметики.
func TestBalances_showTheKindsSeparately(t *testing.T) {
	acc := accountsStub(t)
	acc.list = append(acc.list, namedAccount(t, "Долг → Отец", "3000"))
	m, _ := balanceModel(acc)

	view := press(m, tab()).View()

	for _, want := range []string{
		"Долг",     // заголовок рода
		"Отец",     // короткое имя внутри рода
		"свободно", // сколько из итога лежит на карте
	} {
		if !strings.Contains(view, want) {
			t.Errorf("на экране нет %q\n--- view ---\n%s", want, view)
		}
	}
	// Полного имени в строке быть не должно: род уже назван заголовком.
	if strings.Contains(view, "Долг → Отец") {
		t.Errorf("строка повторяет род, уже написанный заголовком\n--- view ---\n%s", view)
	}
}
