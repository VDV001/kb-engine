package financevocab_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
