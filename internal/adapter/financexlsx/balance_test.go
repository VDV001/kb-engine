package financexlsx_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// Написание счета решает домен, а не побайтовое равенство.
//
// Три пути к одному листу «Счета» сравнивали имя по-разному: заведение счета
// (--create) спрашивало домен, а запись баланса и отчет о прежнем значении —
// побайтово. Следствие видно человеку: незнакомое написание счета, который на
// листе стоит, заводить отказываются («счет уже есть»), и обновить тем же
// написанием тоже отказываются («такого счета нет»). Оба ответа про один счет.
func TestSetBalance_matchesTheAccountTheWayTheDomainDoes(t *testing.T) {
	for _, spelling := range []string{"сбербанк", " Сбербанк ", "СБЕРБАНК"} {
		path := workbookWithExtraColumn(t)
		if err := financexlsx.SetBalance(path, spelling, money(t, "4321.55"), time.Now); err != nil {
			t.Errorf("SetBalance(%q) = %v — счет должен найтись правилом домена", spelling, err)
		}
	}
}

// Отказ по-прежнему отказывает: правило домена терпимо к регистру и пробелам,
// но не превращает чужое имя в свое. Без этой проверки предыдущая прошла бы и
// на сравнении, которое сходится всегда.
func TestSetBalance_stillRefusesAnUnknownAccount(t *testing.T) {
	path := workbookWithExtraColumn(t)

	if err := financexlsx.SetBalance(path, "Озон Банк", money(t, "500.00"), time.Now); err == nil {
		t.Error("неизвестный счет принят, ожидался отказ")
	}
}
