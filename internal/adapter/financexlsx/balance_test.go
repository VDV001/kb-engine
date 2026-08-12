package financexlsx_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// Написание счёта решает домен, а не побайтовое равенство.
//
// Три пути к одному листу «Счета» сравнивали имя по-разному: заведение счёта
// (--create) спрашивало домен, а запись баланса и отчёт о прежнем значении —
// побайтово. Следствие видно человеку: незнакомое написание счёта, который на
// листе стоит, заводить отказываются («счёт уже есть»), и обновить тем же
// написанием тоже отказываются («такого счёта нет»). Оба ответа про один счёт.
func TestSetBalance_matchesTheAccountTheWayTheDomainDoes(t *testing.T) {
	for _, spelling := range []string{"сбербанк", " Сбербанк ", "СБЕРБАНК"} {
		path := workbookWithExtraColumn(t)
		if err := financexlsx.SetBalance(path, spelling, money(t, "4321.55"), time.Now); err != nil {
			t.Errorf("SetBalance(%q) = %v — счёт должен найтись правилом домена", spelling, err)
		}
	}
}

// Отказ по-прежнему отказывает: правило домена терпимо к регистру и пробелам,
// но не превращает чужое имя в своё. Без этой проверки предыдущая прошла бы и
// на сравнении, которое сходится всегда.
func TestSetBalance_stillRefusesAnUnknownAccount(t *testing.T) {
	path := workbookWithExtraColumn(t)

	if err := financexlsx.SetBalance(path, "Озон Банк", money(t, "500.00"), time.Now); err == nil {
		t.Error("неизвестный счёт принят, ожидался отказ")
	}
}
