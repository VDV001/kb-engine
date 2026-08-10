package balancestate_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/balancestate"
)

// Момент подтверждения переживает запись и чтение. Ради этого файл и заведён:
// лист «Счета» хранит день, а внутри дня решает момент.
func TestRecordThenLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".balance-state.json")
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.FixedZone("книга", 5*60*60))

	if err := balancestate.Record(path, "Сбербанк", at); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := balancestate.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	moment, known := got.At("Сбербанк")
	if !known {
		t.Fatal("момент не прочитался")
	}
	if !moment.Equal(at) {
		t.Errorf("момент = %s, ожидался %s", moment, at)
	}
}

// Файла нет — это не сбой. У книги, которую ни разу не подтверждали через
// движок, состояния нет, и расчёт обязан продолжить работать по прежнему
// правилу, а не отказать целиком.
func TestLoadMissingFileIsEmptyNotAnError(t *testing.T) {
	got, err := balancestate.Load(filepath.Join(t.TempDir(), "нет-такого.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("прочитано %d записей, ожидалось 0", len(got))
	}
}

// Битый файл — это уже сбой, и молчать о нём нельзя: расчёт вернётся к прежнему
// правилу и завысит остаток, а человек будет думать, что смотрит на точное
// число. Пустой ответ и испорченный файл — разные ответы.
func TestLoadReportsABrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".balance-state.json")
	if err := os.WriteFile(path, []byte("{не json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := balancestate.Load(path); err == nil {
		t.Error("испорченный файл прочитался без ошибки")
	}
}

// Подтверждение одного счёта не стирает то, что известно про остальные.
func TestRecordKeepsTheOtherAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".balance-state.json")
	first := time.Date(2026, 8, 4, 14, 18, 0, 0, time.UTC)
	second := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	if err := balancestate.Record(path, "Альфа-Банк", first); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := balancestate.Record(path, "Сбербанк", second); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := balancestate.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if moment, known := got.At("Альфа-Банк"); !known || !moment.Equal(first) {
		t.Errorf("Альфа-Банк = %s (известен=%v), ожидался %s", moment, known, first)
	}
	if moment, known := got.At("Сбербанк"); !known || !moment.Equal(second) {
		t.Errorf("Сбербанк = %s (известен=%v), ожидался %s", moment, known, second)
	}
}

// Повторное подтверждение того же счёта заменяет момент, а не добавляет второй.
// Иначе расчёт однажды сравнил бы трату с давно устаревшим подтверждением.
func TestRecordReplacesTheMomentOfTheSameAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".balance-state.json")
	older := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 10, 18, 30, 0, 0, time.UTC)

	for _, at := range []time.Time{older, newer} {
		if err := balancestate.Record(path, "Сбербанк", at); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	got, err := balancestate.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("записей %d, ожидалась 1", len(got))
	}
	if moment, _ := got.At("Сбербанк"); !moment.Equal(newer) {
		t.Errorf("момент = %s, ожидался последний %s", moment, newer)
	}
}

// Написание счёта решает домен и здесь: «долг→отец», записанный терминалом,
// и «Долг → Отец» с листа «Счета» — один счёт, иначе у него окажется две
// записи и расчёт возьмёт ту, что попалась.
func TestRecordMatchesTheAccountTheWayTheDomainDoes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".balance-state.json")
	older := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	if err := balancestate.Record(path, "Долг → Отец", older); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := balancestate.Record(path, "долг→отец", newer); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := balancestate.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("записей %d, ожидалась 1 — это один и тот же счёт", len(got))
	}
	if moment, _ := got.At("Долг → Отец"); !moment.Equal(newer) {
		t.Errorf("момент = %s, ожидался %s", moment, newer)
	}
}

// Состояние лежит рядом с книгой, потому что подтверждают остаток счёта, а
// счета живут в книге. Команда `fin balance` другого пути и не знает: у неё
// есть --from и нет --ledger.
func TestPathNextToTheWorkbook(t *testing.T) {
	got := balancestate.PathNextTo(filepath.Join("/дом", "finances", "Учёт_финансов.xlsx"))
	want := filepath.Join("/дом", "finances", ".balance-state.json")
	if got != want {
		t.Errorf("путь = %s, ожидался %s", got, want)
	}
}
