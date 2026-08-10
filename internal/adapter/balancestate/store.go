// Package balancestate stores when each account balance was confirmed, next to
// the workbook that holds the balances themselves.
//
// Лист «Счета» хранит ДЕНЬ подтверждения, и это правильно: колонку читает
// человек, а человеку нужен день. Расчёту же дня не хватает — траты того же дня
// делятся на записанные до того, как владелец посмотрел в приложение банка, и
// после, и вычитать нужно только вторые. Момент живёт здесь, чтобы книга
// осталась читаемой, а расчёт перестал гадать.
package balancestate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// FileName — имя состояния рядом с книгой, по образцу состояния синхронизации.
const FileName = ".balance-state.json"

// PathNextTo returns where the state lives for a given workbook.
//
// Рядом с книгой, а не с журналом: подтверждают остаток счёта, счета живут в
// книге, и команда `fin balance` знает только путь к ней — у неё есть --from и
// нет --ledger.
func PathNextTo(workbookPath string) string {
	return filepath.Join(filepath.Dir(workbookPath), FileName)
}

// file is the shape on disk: account names as the sheet spells them, so the
// file stays readable next to the workbook it describes.
type file struct {
	Confirmed map[string]time.Time `json:"confirmed"`
}

// Load reads the confirmation moments.
//
// Отсутствующий файл — пустое состояние, а не сбой: у книги, которую ни разу не
// подтверждали через движок, момента нет, и расчёт обязан продолжить работать
// по прежнему правилу. Испорченный файл — наоборот сбой, и молчать о нём
// нельзя: расчёт вернулся бы к приблизительному правилу молча, а человек
// смотрел бы на завышенное число как на точное.
func Load(path string) (finance.Confirmations, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return finance.Confirmations{}, nil
	}
	if err != nil {
		return finance.Confirmations{}, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return finance.Confirmations{}, fmt.Errorf("%s: %w", path, err)
	}
	out := make(finance.Confirmations, len(f.Confirmed))
	for bank, at := range f.Confirmed {
		out[bank] = at
	}
	return out, nil
}

// Record remembers when this account was confirmed, keeping the other accounts.
//
// Счёт ищется правилом домена: «долг→отец» из терминала и «Долг → Отец» с листа
// «Счета» — один счёт, и две записи о нём означали бы, что расчёт возьмёт ту,
// которая попалась первой в карте.
func Record(path, bank string, at time.Time) error {
	f := file{Confirmed: map[string]time.Time{}}
	switch raw, err := os.ReadFile(path); {
	case err == nil:
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if f.Confirmed == nil {
			f.Confirmed = map[string]time.Time{}
		}
	case os.IsNotExist(err):
		// Первое подтверждение через движок создаёт состояние.
	default:
		return err
	}

	for name := range f.Confirmed {
		if domain.SameAccountName(name, bank) {
			delete(f.Confirmed, name)
		}
	}
	f.Confirmed[bank] = at

	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}
