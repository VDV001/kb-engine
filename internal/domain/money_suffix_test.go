package domain_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Человек пишет сумму так, как говорит: «418р», «565 руб». Знак ₽ разбор уже
// принимает, а буквенную запись отвергал — при том что это то же самое число,
// и отказ приходил на самом частом способе его набрать.
func TestParseMoney_acceptsWrittenRubles(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int64
	}{
		{"418р", 41800},
		{"418 р", 41800},
		{"418р.", 41800},
		{"565,44руб", 56544},
		{"565.44 руб", 56544},
		{"1 500 рублей", 150000},
		{"418₽", 41800},
		{"418 ₽", 41800},
	} {
		t.Run(c.raw, func(t *testing.T) {
			m, err := domain.ParseMoney(c.raw)
			if err != nil {
				t.Fatalf("ParseMoney(%q): %v", c.raw, err)
			}
			if m.Kopecks() != c.want {
				t.Errorf("ParseMoney(%q) = %d копеек, ожидалось %d", c.raw, m.Kopecks(), c.want)
			}
		})
	}
}

// Буква валюты не должна превращать мусор в число: «р» отдельно, «4р1р8» и
// прочее — по-прежнему отказ, иначе очистка суффикса начнёт чинить опечатки.
func TestParseMoney_stillRefusesNonsense(t *testing.T) {
	for _, raw := range []string{"р", "руб", "₽", "4р18", "418рр", "418 рубл"} {
		t.Run(raw, func(t *testing.T) {
			if m, err := domain.ParseMoney(raw); err == nil {
				t.Errorf("ParseMoney(%q) принят как %s, ожидался отказ", raw, m)
			}
		})
	}
}
