package main

import (
	"bytes"
	"strings"
	"testing"
)

// Счётчик вызовов виден в отчёте — иначе журнал пишется, а ответить «сколько
// раз агент спрашивал базу» по-прежнему нечем.
//
// ⚠️ И тем же тестом проверяется, чего в отчёте быть НЕ ДОЛЖНО: текста запроса.
// Запрос лежит в аргументах, а правило файла — имена и числа, но не значения:
// «финансы за июль» в поиске раскрывает предмет интереса ровно так же, как
// сумма в `fin add`.
func TestRunsReport_countsMCPCallsWithoutShowingQueries(t *testing.T) {
	path := journalWith(t,
		`{"command":"mcp:search_catalog","args":["зарплата и переезд"],`+
			`"started_at":"2026-08-28T10:00:00+05:00","took_ms":12,"exit_code":0}`,
		`{"command":"mcp:search_catalog","args":["ddd"],`+
			`"started_at":"2026-08-28T10:05:00+05:00","took_ms":9,"exit_code":0}`,
		`{"command":"mcp:get_entry","args":["#9999"],`+
			`"started_at":"2026-08-28T10:06:00+05:00","took_ms":3,"exit_code":1}`,
		`{"command":"audit","args":["--check","versions"],`+
			`"started_at":"2026-08-28T09:00:00+05:00","took_ms":40,"exit_code":0}`)

	var out, errOut bytes.Buffer
	if code := run([]string{"runs", "--journal", path}, &out, &errOut); code != 0 {
		t.Fatalf("runs вернул %d: %s", code, errOut.String())
	}
	got := out.String()

	for _, want := range []string{"MCP", "search_catalog", "get_entry"} {
		if !strings.Contains(got, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, got)
		}
	}
	// Два вызова поиска и один промах по id: числа — то, ради чего счётчик.
	if !strings.Contains(got, "вызовов 2") {
		t.Errorf("не назван счёт вызовов поиска:\n%s", got)
	}
	for _, secret := range []string{"зарплата и переезд", "ddd", "#9999"} {
		if strings.Contains(got, secret) {
			t.Errorf("в отчёт утекло значение аргумента %q:\n%s", secret, got)
		}
	}
	// Правило 11: отчёт обязан назвать, чего он про вызовы НЕ знает. Переходы
	// по ссылке view видит браузер владельца, а не движок, и молчание об этом
	// читалось бы как «ссылками не пользуются».
	if !strings.Contains(got, "view") {
		t.Errorf("не сказано, что переходы по ссылке view движку не видны:\n%s", got)
	}
	// Вызов инструмента не команда движка: он не обязан появляться там, где
	// перечисляются никогда не запускавшиеся команды.
	if strings.Contains(got, "mcp:") {
		t.Errorf("приставка хранения показана человеку:\n%s", got)
	}
}
