package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// journalWith кладёт готовый журнал во временный файл и отдаёт путь.
//
// ⚠️ Каталог назван так намеренно. Отчёт печатает путь к журналу, а `t.TempDir()`
// оканчивается случайным десятизначным числом — примерно один прогон из ста
// двадцати пяти содержал в нём «418» и красил проверку утечки ложно. Проверка,
// краснеющая случайно, перестаёт читаться, поэтому ловушка сделана постоянной:
// путь несёт и сумму, и счёт из фикстуры.
func journalWith(t *testing.T, lines ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "418.50-Сбербанк")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runs.jsonl")
	var body strings.Builder
	for _, l := range lines {
		body.WriteString(l + "\n")
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ⚠️ Главная проверка файла. В аргументах журнала лежат настоящие суммы и
// места владельца, и хранит он их осознанно — файл вне любого репозитория.
// Но показ и хранение разные вопросы: отчёт оперирует именами команд.
func TestRunsReport_neverShowsArgumentValues(t *testing.T) {
	path := journalWith(t,
		`{"command":"fin","args":["add","--amount","418.50","--place","Такси Юрент","--account","Сбербанк"],`+
			`"started_at":"2026-08-18T10:00:00+05:00","took_ms":7,"exit_code":0}`)

	var out, errOut bytes.Buffer
	if code := run([]string{"runs", "--journal", path}, &out, &errOut); code != 0 {
		t.Fatalf("код %d, stderr: %s", code, errOut.String())
	}

	// Путь к журналу отчёт называет намеренно, и он не является значением
	// аргумента: ищем утечку во всём остальном, а сам путь заменяем меткой.
	// Без этой замены проверка ловила бы имя каталога, которое выбрала не она.
	got := strings.ReplaceAll(out.String()+errOut.String(), path, "<журнал>")
	for _, secret := range []string{"418.50", "418", "Такси Юрент", "Сбербанк"} {
		if strings.Contains(got, secret) {
			t.Errorf("отчёт показал значение аргумента %q:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "fin") {
		t.Errorf("имя команды в отчёт не попало:\n%s", got)
	}
}

// Набор известных команд берётся из карты диспетчера, а не из списка, набранного
// в отчёте руками: список расходится с кодом молча, и тогда новая команда
// никогда не попадёт в «не запускалась ни разу».
func TestRunsReport_knownCommandsComeFromTheDispatcher(t *testing.T) {
	path := journalWith(t)

	var out, errOut bytes.Buffer
	if code := run([]string{"runs", "--journal", path}, &out, &errOut); code != 0 {
		t.Fatalf("код %d, stderr: %s", code, errOut.String())
	}

	got := out.String()
	for name := range commands {
		if name == "runs" {
			continue
		}
		if !strings.Contains(got, name) {
			t.Errorf("команда %q не названа среди незапускавшихся:\n%s", name, got)
		}
	}
}

func TestRunsReport_absenceCases(t *testing.T) {
	tests := []struct {
		name     string
		path     func(t *testing.T) string
		wantAny  []string
		wantNone []string
	}{
		{
			// Журнала нет вовсе — сказать «прогонов не было» здесь значит
			// соврать: движок мог работать сборкой, которая журнала не писала.
			name:    "журнала нет",
			path:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "нет.jsonl") },
			wantAny: []string{"журнал", "не найден"},
		},
		{
			name:    "журнал есть и пуст",
			path:    func(t *testing.T) string { return journalWith(t) },
			wantAny: []string{"ни одного прогона"},
			// «Журнала нет» и «журнал пуст» обязаны читаться по-разному.
			wantNone: []string{"не найден"},
		},
		{
			// Правило 11: отчёт называет, чего он не умеет. Иначе молчание про
			// замедление читается как «замедления нет».
			name: "названо, чего отчёт не проверяет",
			path: func(t *testing.T) string {
				return journalWith(t, `{"command":"audit","args":["--check","all"],`+
					`"started_at":"2026-08-10T10:00:00+05:00","took_ms":120,"exit_code":0}`)
			},
			wantAny: []string{"не проверяет"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run([]string{"runs", "--journal", tt.path(t)}, &out, &errOut); code != 0 {
				t.Fatalf("код %d, stderr: %s", code, errOut.String())
			}
			got := strings.ToLower(out.String() + errOut.String())
			for _, want := range tt.wantAny {
				if !strings.Contains(got, strings.ToLower(want)) {
					t.Errorf("в отчёте нет %q:\n%s", want, got)
				}
			}
			for _, none := range tt.wantNone {
				if strings.Contains(got, strings.ToLower(none)) {
					t.Errorf("в отчёте нашлось лишнее %q:\n%s", none, got)
				}
			}
		})
	}
}

// Нечитаемая строка обязана быть названа: инвентарь, молчащий о том, чего он не
// смог прочитать, выдаёт неполноту за полноту.
func TestRunsReport_countsUnreadableLines(t *testing.T) {
	path := journalWith(t,
		`{"command":"drift","started_at":"2026-08-11T10:00:00+05:00","took_ms":3,"exit_code":0}`,
		`это не json`)

	var out, errOut bytes.Buffer
	if code := run([]string{"runs", "--journal", path}, &out, &errOut); code != 0 {
		t.Fatalf("код %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String()+errOut.String(), "нечитаем") {
		t.Errorf("нечитаемая строка не названа:\n%s", out.String())
	}
}
