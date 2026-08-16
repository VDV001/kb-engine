package financevocab_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financevocab"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance-aliases.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// Ключи в файле человек пишет как ему удобно — «Сбер», «Т-Банк», «Живика».
// Сравнение при разборе идёт по нормализованному слову, поэтому нормализует их
// загрузка: иначе строка «418 сбер» не нашла бы ключ «Сбер», записанный руками.
func TestLoad_normalizesKeys(t *testing.T) {
	path := write(t, `{
	  "accounts": {"Сбер": "Сбербанк", "Т-Банк": "Т-Банк"},
	  "places": {"Живика": {"category": "Здоровье", "subcategory": "Аптека", "place": "Живика"}}
	}`)

	v, err := financevocab.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := v.Accounts["сбер"]; got != "Сбербанк" {
		t.Errorf("accounts[сбер] = %q, ожидалось Сбербанк", got)
	}
	if got := v.Accounts["тбанк"]; got != "Т-Банк" {
		t.Errorf("accounts[тбанк] = %q, ожидалось Т-Банк — дефис не должен мешать", got)
	}
	if got := v.Places["живика"].Subcategory; got != "Аптека" {
		t.Errorf("places[живика].subcategory = %q, ожидалась Аптека", got)
	}
}

// Словаря может не быть — это нормальное состояние, а не поломка. Но молчать о
// нём нельзя: без словаря быстрый ввод узнаёт только сумму, и человек должен
// понимать, почему.
func TestLoad_missingFileIsNamed(t *testing.T) {
	v, err := financevocab.Load(filepath.Join(t.TempDir(), "нет-такого.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load без файла вернул %v, ожидалась fs.ErrNotExist", err)
	}
	if len(v.Accounts) != 0 || len(v.Places) != 0 {
		t.Errorf("словарь без файла не пуст: %+v", v)
	}
}

// Запомненное слово должно пережить перезапуск и не затереть остальные.
func TestRememberPlace_keepsWhatWasThere(t *testing.T) {
	path := write(t, `{"accounts": {"Сбер": "Сбербанк"}, "places": {"Магнит": {"category": "Еда", "subcategory": "Продукты", "place": "Магнит"}}}`)

	err := financevocab.RememberPlace(path, "Живика", finance.PlaceRule{
		Category: "Здоровье", Subcategory: "Аптека", Place: "Живика",
	})
	if err != nil {
		t.Fatalf("RememberPlace: %v", err)
	}

	v, err := financevocab.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := v.Places["живика"].Category; got != "Здоровье" {
		t.Errorf("новое слово не запомнилось: %q", got)
	}
	if got := v.Places["магнит"].Place; got != "Магнит" {
		t.Errorf("старое слово потеряно: %q", got)
	}
	if got := v.Accounts["сбер"]; got != "Сбербанк" {
		t.Errorf("счета потеряны при записи места: %q", got)
	}
}

// Файла нет — запомнить всё равно можно: первое слово создаёт словарь.
func TestRememberAccount_createsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "finance-aliases.json")

	if err := financevocab.RememberAccount(path, "Втб", "ВТБ"); err != nil {
		t.Fatalf("RememberAccount: %v", err)
	}

	v, err := financevocab.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := v.Accounts["втб"]; got != "ВТБ" {
		t.Errorf("accounts[втб] = %q, ожидалось ВТБ", got)
	}
}

// Два ключа, различающиеся только регистром (или «ё», или дефисом), дают одну
// нормальную форму. Молча оставить один из них нельзя: какой победит, решает
// порядок обхода карты при разборе JSON, то есть выбор случаен от запуска к
// запуску. Для словаря, который существует ровно затем, чтобы написание было
// одинаковым, это худший отказ — он не ломается, он тихо расходится.
func TestLoad_namesConflictingKeys(t *testing.T) {
	path := write(t, `{
	  "accounts": {},
	  "places": {
	    "КБ":  {"category": "Продукты", "subcategory": "Магазин", "place": "К&Б"},
	    "кб":  {"category": "Развлечения", "subcategory": "Бар", "place": "КБ"}
	  }
	}`)

	v, err := financevocab.Load(path)
	if !errors.Is(err, financevocab.ErrConflict) {
		t.Fatalf("err = %v, ожидалась ErrConflict", err)
	}
	// Названы обе исходные формы: без них человеку негде искать в файле.
	for _, want := range []string{"КБ", "кб"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("сообщение %q не называет ключ %q", err.Error(), want)
		}
	}
	// Спорный ключ не подставляется вовсе: выбрать одно из двух правил значило бы
	// оставить ту же монетку, только брошенную один раз при загрузке.
	if _, ok := v.Places["кб"]; ok {
		t.Errorf("спорный ключ остался в словаре: %+v", v.Places["кб"])
	}
	// Остальной словарь при этом работает — из-за одной пары нельзя лишать
	// человека записи расходов.
	if len(v.Accounts) != 0 {
		t.Errorf("accounts = %v, ожидался пустой", v.Accounts)
	}
}

// Два написания одного и того же правила — не конфликт, а именно то, ради чего
// словарь и держит человеческие ключи: «Пятерочка» и «Пятёрочка» пишутся
// по-разному, а значат одно.
func TestLoad_collapsesIdenticalRules(t *testing.T) {
	path := write(t, `{
	  "accounts": {"Сбер": "Сбербанк", "сбер": "Сбербанк"},
	  "places": {
	    "Пятерочка": {"category": "Продукты", "subcategory": "Магазин", "place": "Пятёрочка"},
	    "Пятёрочка": {"category": "Продукты", "subcategory": "Магазин", "place": "Пятёрочка"}
	  }
	}`)

	v, err := financevocab.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := v.Accounts["сбер"]; got != "Сбербанк" {
		t.Errorf("accounts[сбер] = %q", got)
	}
	if got := v.Places["пятерочка"].Place; got != "Пятёрочка" {
		t.Errorf("places[пятерочка].Place = %q", got)
	}
}

// Конфликты в счетах ловятся так же, как в местах: путь один, а ошибка в счёте
// дороже — она решает, с какой карты списаны деньги.
func TestLoad_namesConflictingAccounts(t *testing.T) {
	path := write(t, `{
	  "accounts": {"Сбер": "Сбербанк", "сбер": "Альфа-Банк"},
	  "places": {}
	}`)

	v, err := financevocab.Load(path)
	if !errors.Is(err, financevocab.ErrConflict) {
		t.Fatalf("err = %v, ожидалась ErrConflict", err)
	}
	if _, ok := v.Accounts["сбер"]; ok {
		t.Errorf("спорный счёт остался в словаре: %q", v.Accounts["сбер"])
	}
	if !strings.Contains(err.Error(), "Сбербанк") || !strings.Contains(err.Error(), "Альфа-Банк") {
		t.Errorf("сообщение %q не называет оба правила", err.Error())
	}
}
