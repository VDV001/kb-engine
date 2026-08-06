package finance_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Одно число «итого по счетам» отвечает не на тот вопрос, который задают, глядя
// на него. 150 000 на заморозке и 3 000, которых нет на карте, потому что их
// занял человек, — это не те же деньги, что лежат на карте сейчас.
//
// Группа берётся из имени счёта: стрелка в книге уже значит «одно из нескольких
// одного рода», и заводить ради этого второе поле значило бы держать признак в
// двух местах и однажды разойтись.
func TestTotalsByGroup(t *testing.T) {
	balances := []finance.AccountBalance{
		{Bank: "Сбербанк", Current: money(t, "2000")},
		{Bank: "Резерв → Наличные", Current: money(t, "150000")},
		{Bank: "Альфа-Банк", Current: money(t, "1000")},
		{Bank: "Займ → Коллеге", Current: money(t, "3000")},
		{Bank: "Резерв → Депозит", Current: money(t, "0")},
	}

	got := finance.TotalsByGroup(balances)

	want := []struct {
		group string
		total string
	}{
		// Счета без группы идут первыми и одной строкой: это деньги, которыми
		// человек располагает сейчас, и ради них экран открывают.
		{"", "3000.00"},
		{"Резерв", "150000.00"},
		{"Займ", "3000.00"},
	}
	if len(got) != len(want) {
		t.Fatalf("групп = %d, ожидалось %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Group != w.group {
			t.Errorf("группа %d = %q, ожидалась %q", i, got[i].Group, w.group)
		}
		if got[i].Total.String() != w.total {
			t.Errorf("группа %q: сумма = %s, ожидалась %s", w.group, got[i].Total, w.total)
		}
	}
}

// Порядок групп — тот, в котором они встретились в книге, а не алфавитный:
// владелец сам решает, что за чем идёт на листе, и витрина не переставляет его
// строки.
func TestTotalsByGroup_keepsTheOrderOfTheBook(t *testing.T) {
	balances := []finance.AccountBalance{
		{Bank: "Займ → Коллеге", Current: money(t, "3000")},
		{Bank: "Резерв → Наличные", Current: money(t, "150000")},
	}

	got := finance.TotalsByGroup(balances)
	if len(got) != 2 || got[0].Group != "Займ" || got[1].Group != "Резерв" {
		t.Fatalf("порядок групп = %+v, ожидались Займ, затем Резерв", got)
	}
}

// Пустой список счетов даёт пустой ответ, а не строку «нет группы» с нулём:
// ноль, которого никто не подтверждал, читается как факт и им не является.
func TestTotalsByGroup_emptyStaysEmpty(t *testing.T) {
	if got := finance.TotalsByGroup(nil); len(got) != 0 {
		t.Errorf("на пустом списке вернулось %+v", got)
	}
}

// Расчёт остатка обязан донести группу до витрин: иначе каждая витрина будет
// разбирать имя счёта сама, и одна из них однажды разберёт иначе.
func TestCurrentBalances_carriesTheGroup(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC) }
	acc, err := domain.NewAccount("Займ → Коллеге", domain.NewMoney(300000), now(), now)
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}

	got := finance.CurrentBalances([]domain.Account{acc}, nil)
	if len(got) != 1 {
		t.Fatalf("счетов = %d", len(got))
	}
	if got[0].Group != "Займ" {
		t.Errorf("Group = %q, ожидалось «Долг»", got[0].Group)
	}
	if got[0].NameWithinGroup != "Коллеге" {
		t.Errorf("NameWithinGroup = %q, ожидалось «Отец»", got[0].NameWithinGroup)
	}
}

func money(t *testing.T, raw string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(raw)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", raw, err)
	}
	return m
}
