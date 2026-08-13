package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Восемь одновременных записей должны дать восемь строк.
//
// Запись журнала — чтение-правка-запись: appendChecked читает файл целиком,
// проверяет повтор по прочитанному и перезаписывает всё. Атомарная замена
// бережёт от обрывка файла, но не от потерянного обновления: процессы,
// прочитавшие одно состояние, запишут два разных, и победит последний.
//
// «Единственный путь записи» защищает ПУТЬ, а не ФАЙЛ: одна функция, вызванная
// одновременно дважды, — всё ещё два писателя. У этой установки два процесса
// живут рядом постоянно: `kb` (TUI, висит открытым) и `kbadd` из другого окна.
//
// ⚠️ Замер до починки, три прогона из трёх одинаково: из восьми записей в файл
// доезжала ОДНА, и все восемь команд отчитывались успехом. Молчаливая потеря —
// худший вид: человек видит «записано» и уходит.
func TestFinAdd_concurrentWritesAllLand(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)
	before := ledgerLines(t, ledger)

	const n = 8
	type outcome struct {
		code int
		msg  string
	}
	results := make(chan outcome, n)
	for i := range n {
		go func(i int) {
			var out, errb bytes.Buffer
			code := run([]string{
				"fin", "add", "--ledger", ledger,
				"--amount", fmt.Sprintf("%d", 100+i), "--cat", "Еда",
				"--place", fmt.Sprintf("Место%d", i), "--date", "2026-04-06",
			}, &out, &errb)
			results <- outcome{code, errb.String()}
		}(i)
	}

	succeeded := 0
	for range n {
		r := <-results
		if r.code == 0 {
			succeeded++
			continue
		}
		t.Logf("команда отказала (код %d): %s", r.code, strings.TrimSpace(r.msg))
	}

	after := ledgerLines(t, ledger)
	// Проверяются обе стороны сразу: сколько команд сказали «записано» и сколько
	// строк в файле. Порознь каждая цифра выглядит нормально — врёт их разница.
	if added := after - before; added != succeeded {
		t.Errorf("успехом отчиталось %d команд, в файле прибавилось %d строк — потеряно %d",
			succeeded, added, succeeded-added)
	}
	if succeeded != n {
		t.Errorf("успешных команд %d из %d", succeeded, n)
	}
}

func ledgerLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	trimmed := strings.TrimRight(string(raw), "\n")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}
