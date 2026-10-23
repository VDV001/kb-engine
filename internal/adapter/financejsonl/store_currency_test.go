package financejsonl_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Валюта записи переживает круг «записал — прочитал», а рубль не пишется вовсе:
// так журнал, заведённый до поля, и новый рублёвый журнал читаются одинаково, и
// в строке не появляется ничего, чего человек раньше там не видел.
func TestSaveLoad_currency(t *testing.T) {
	usd, err := domain.NewCurrency("USD")
	if err != nil {
		t.Fatalf("NewCurrency: %v", err)
	}
	foreign := record(t, "01C", domain.TransactionParams{
		Kind:     domain.KindExpense,
		Date:     time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		Amount:   domain.NewMoney(1230),
		Category: "Сервисы",
		Account:  "Банк USD",
		Currency: usd,
	})
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	if err := financejsonl.Save(path, []finance.Record{expense(t, "01A"), foreign}, savedAt); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if strings.Contains(lines[0], `"currency"`) {
		t.Errorf("рублёвая запись не должна нести поле currency:\n%s", lines[0])
	}
	if !strings.Contains(lines[1], `"currency":"USD"`) {
		t.Errorf("долларовая запись обязана нести currency:\n%s", lines[1])
	}

	got, err := financejsonl.Load(path, loadedAt)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c := got[0].Transaction().Currency(); !c.IsBase() {
		t.Errorf("рублёвая запись прочитана в %s", c.Code())
	}
	if c := got[1].Transaction().Currency().Code(); c != "USD" {
		t.Errorf("долларовая запись прочитана в %s", c)
	}
}

// Негодный код в журнале — отказ, а не рубль. Молчаливое «не разобрал — значит
// рубль» показало бы долларовую трату рублёвой, ровно тот дефект, ради которого
// поле заведено.
func TestLoad_rejectsAnUnreadableCurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	line := `{"id":"01A","kind":"expense","date":"2026-03-29","amount":"202.45","category":"Еда","currency":"ДОЛ","rev":1,"updated_at":"2026-07-29T02:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	// Отказ обязан быть ИЗ-ЗА ВАЛЮТЫ. Журнал, не знающий поля, тоже откажет —
	// по неизвестному ключу, — и тест, проверяющий только «ошибка есть», прошёл
	// бы на коде без поддержки валюты вовсе. Так он и прошёл в первый раз.
	_, err := financejsonl.Load(path, loadedAt)
	if !errors.Is(err, domain.ErrInvalidCurrency) {
		t.Fatalf("Load = %v, want ErrInvalidCurrency", err)
	}
}
