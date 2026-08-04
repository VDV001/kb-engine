package tui_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// stubAccounts stands in for the workbook's Счета sheet: what the banks say
// today, and the one way to change it.
type stubAccounts struct {
	list      []domain.Account
	got       []balanceCall
	created   []balanceCall
	err       error
	createErr error
}

type balanceCall struct {
	bank   string
	amount domain.Money
}

func (s *stubAccounts) Accounts() ([]domain.Account, error) { return s.list, s.err }

// Balances отдаёт то же, что посчитал бы usecase на этих счетах без трат:
// стаб стоит вместо книги, а не вместо арифметики.
func (s *stubAccounts) Balances() ([]finance.AccountBalance, error) {
	if s.err != nil {
		return nil, s.err
	}
	return finance.CurrentBalances(s.list, nil), nil
}

// AddAccount заводит счёт, которого на листе ещё нет. Отдельный метод, а не
// флаг у SetBalance: экран обязан различать «поправил число» и «пополнил
// словарь, решающий, что вообще считается счётом».
func (s *stubAccounts) AddAccount(bank string, amount domain.Money) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, balanceCall{bank, amount})
	return nil
}

func (s *stubAccounts) SetBalance(bank string, amount domain.Money) error {
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, balanceCall{bank, amount})
	return nil
}

func accountsStub(t *testing.T) *stubAccounts {
	t.Helper()
	at := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC) }
	var list []domain.Account
	for _, p := range []struct {
		bank string
		sum  string
	}{{"Сбербанк", "1000.50"}, {"Альфа-Банк", "1507.12"}} {
		m, err := domain.ParseMoney(p.sum)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", p.sum, err)
		}
		acc, err := domain.NewAccount(p.bank, m, at, clock)
		if err != nil {
			t.Fatalf("NewAccount(%q): %v", p.bank, err)
		}
		list = append(list, acc)
	}
	return &stubAccounts{list: list}
}

func balanceModel(acc *stubAccounts) (tui.Model, *stubWriter) {
	fin := &stubFinances{sum: sampleSummary()}
	w := &stubWriter{}
	return tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(w).WithAccounts(acc), w
}

// The balances belong on the screen that can change them. Writing a number the
// screen never shows means the only way to check the result is to leave for the
// dashboard or the book itself.
func TestBalances_areShownOnTheFinancesScreen(t *testing.T) {
	m, _ := balanceModel(accountsStub(t))

	m = press(m, tab())

	view := m.View()
	// Суммы на экране — в человеческом виде: разряды разделены, копейки через
	// запятую. Хранит и печатает в CLI движок их иначе, и это намеренно.
	for _, want := range []string{"Сбербанк", "1 000,50", "Альфа-Банк", "1 507,12"} {
		if !strings.Contains(view, want) {
			t.Errorf("на экране нет %q\n--- view ---\n%s", want, view)
		}
	}
}

// One balance, typed where it is read. Until this key existed the number could
// only be changed from the CLI or, worse, by a second writer going straight
// into the workbook's cells.
func TestBalances_writesFromTheScreen(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Альфа-Банк") // счёт
	m = press(m, runes("4321,55"))
	m = press(m, enter())

	if len(acc.got) != 1 {
		t.Fatalf("баланс записан %d раз, ожидался 1", len(acc.got))
	}
	if acc.got[0].bank != "Альфа-Банк" {
		t.Errorf("счёт = %q, ожидался Альфа-Банк", acc.got[0].bank)
	}
	if acc.got[0].amount.Kopecks() != 432155 {
		t.Errorf("сумма = %d копеек, ожидалось 432155", acc.got[0].amount.Kopecks())
	}
	// And the screen says so: a write only the stub knows about is one the
	// person in front of the terminal cannot confirm.
	if m.OnBalanceForm() {
		t.Error("форма осталась открытой после записи")
	}
	if view := m.View(); !strings.Contains(view, "баланс: Альфа-Банк") {
		t.Errorf("экран не назвал записанный баланс\n--- view ---\n%s", view)
	}
}

// A refused write is named and costs nothing typed: the bank the book does not
// know is a correctable mistake, not a reason to start over.
func TestBalances_keepsTheTypingWhenTheWriteFails(t *testing.T) {
	acc := accountsStub(t)
	acc.err = errors.New("Озон Банк — the Счета sheet lists Сбербанк, Альфа-Банк")
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Озон Банк")
	m = press(m, runes("500"))
	m = press(m, enter())

	view := m.View()
	if !strings.Contains(view, "Счета") {
		t.Errorf("экран не назвал причину отказа\n--- view ---\n%s", view)
	}
	for _, want := range []string{"Озон Банк", "500"} {
		if !strings.Contains(view, want) {
			t.Errorf("экран потерял набранное %q\n--- view ---\n%s", want, view)
		}
	}
}

// An amount that does not parse is refused next to the field, and nothing is
// written.
func TestBalances_refusesAnUnparsableAmount(t *testing.T) {
	acc := accountsStub(t)
	m, _ := balanceModel(acc)

	m = press(press(m, tab()), runes("b"))
	m = fill(m, "Сбербанк")
	m = press(m, runes("много"))
	m = press(m, enter())

	if len(acc.got) != 0 {
		t.Fatalf("баланс записан %d раз с неразобранной суммой, ожидалось 0", len(acc.got))
	}
	if view := m.View(); !strings.Contains(view, "сумма") {
		t.Errorf("экран не назвал сумму причиной\n--- view ---\n%s", view)
	}
}

// Without a source of balances the key is absent rather than present and inert,
// the rule every other writing key on this screen already follows.
func TestBalances_keyIsAbsentWithoutASource(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(&stubWriter{})

	m = press(press(m, tab()), runes("b"))

	// The whole caption, not just "b —": that fragment also occurs inside
	// "Tab — назад к поиску", and the first version of this test failed on it.
	if strings.Contains(m.View(), "b — баланс") {
		t.Errorf("экран предлагает клавишу без источника балансов\n--- view ---\n%s", m.View())
	}
	if m.OnBalanceForm() {
		t.Error("форма баланса открылась без источника балансов")
	}
}
