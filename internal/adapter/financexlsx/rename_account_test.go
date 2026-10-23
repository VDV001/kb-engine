package financexlsx_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/balancestate"
	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// Переименование нужно, чтобы перенести счёт в другой род: имя без стрелки —
// обычный счёт, имя со стрелкой — род, и другого способа сказать это книге нет.
// Баланс, дата и валюта обязаны пережить смену имени: меняется слово, а не деньги.
func TestRenameAccount_keepsTheMoneyUnderTheNewName(t *testing.T) {
	path := workbookWithExtraColumn(t)
	if err := financexlsx.AddAccount(path, "Займ → Банк", money(t, "27,61"), currency(t, "USD"), domain.UnknownRate(), fixedClock(2026, 8, 4)); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	if err := financexlsx.RenameAccount(path, "Займ → Банк", "Банк USD", fixedClock(2026, 8, 5)); err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}

	acc := accountOnSheet(t, path, "Банк USD")
	if acc.Balance().Kopecks() != 2761 {
		t.Errorf("баланс = %d копеек, ожидалось 2761", acc.Balance().Kopecks())
	}
	if got := acc.Updated().Format(time.DateOnly); got != "2026-08-04" {
		t.Errorf("дата подтверждения = %s, ожидалась прежняя 2026-08-04: переименование не подтверждает остаток", got)
	}
	if got := acc.Currency().Code(); got != "USD" {
		t.Errorf("валюта = %q, ожидалась USD", got)
	}
	led, err := financexlsx.Read(path, time.Now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, a := range led.Accounts {
		if domain.SameAccountName(a.Bank(), "Займ → Банк") {
			t.Errorf("прежнее имя осталось на листе рядом с новым")
		}
	}
}

// Момент подтверждения хранится по имени рядом с книгой. Не переехав вместе со
// счётом, он молча вернул бы расчёт к приблизительному правилу «по дню».
func TestRenameAccount_movesTheConfirmationMoment(t *testing.T) {
	path := workbookWithExtraColumn(t)
	at := time.Date(2026, 8, 4, 15, 30, 0, 0, time.UTC)
	if err := financexlsx.SetBalance(path, "Т-Банк", money(t, "100"), func() time.Time { return at }); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}

	if err := financexlsx.RenameAccount(path, "Т-Банк", "Т-Банк карта", fixedClock(2026, 8, 5)); err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}

	moments, err := balancestate.Load(balancestate.PathNextTo(path))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, ok := moments.At("Т-Банк карта"); !ok || !got.Equal(at) {
		t.Errorf("момент под новым именем = %v (%v), ожидался %v", got, ok, at)
	}
	if _, ok := moments.At("Т-Банк"); ok {
		t.Errorf("момент остался и под прежним именем")
	}
}

// Отказы обязаны оставлять книгу байт в байт прежней.
//
// Счёт, на который ссылаются строки «Расходов», переименовать нельзя: лист
// «Счета» — словарь, по которому читается колонка счёта, и старое имя в этих
// строках прочиталось бы как источник, а не как счёт. Следующая синхронизация
// отказала бы, а история тихо поменяла бы смысл.
func TestRenameAccount_refuses(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		want     error
	}{
		{"на счёт ссылаются расходы", "Сбербанк", "Сбер", financexlsx.ErrAccountInUse},
		{"новое имя уже занято", "Т-Банк", "альфа банк", financexlsx.ErrAccountExists},
		{"такого счёта нет", "Почта Банк", "Почта", financexlsx.ErrUnknownAccount},
		{"пустое новое имя", "Т-Банк", "  ", domain.ErrInvalidAccount},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := workbookWithExtraColumn(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			err = financexlsx.RenameAccount(path, c.from, c.to, fixedClock(2026, 8, 5))
			if !errors.Is(err, c.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, c.want)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("отказ изменил книгу")
			}
		})
	}
}

func currency(t *testing.T, code string) domain.Currency {
	t.Helper()
	c, err := domain.NewCurrency(code)
	if err != nil {
		t.Fatalf("NewCurrency(%q): %v", code, err)
	}
	return c
}
