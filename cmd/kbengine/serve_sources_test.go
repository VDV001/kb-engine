package main

import (
	"bytes"
	"strings"
	// Пакет объявляет собственный sync (команда `fin sync`), поэтому импорт
	// стандартного идёт под псевдонимом — иначе имена сталкиваются.
	gosync "sync"
	"testing"
	"time"
)

// Правило 11 стандарта harness-engineering-defaults: инструмент обязан вслух
// называть, чего он НЕ проверил. Здесь — чего он не загрузил.
//
// Повод конкретный. Дашборд подняли одним `--catalog`, и четыре вкладки
// Аналитики оказались пустыми, потому что семантический слой подключается
// отдельным флагом. Экран при этом выглядел поломанным, а логи запуска с
// флагом и без были байт в байт одинаковые — по ним отличить «данных нет» от
// «данные не попросили загрузить» было нельзя.
func TestStartupSources(t *testing.T) {
	tests := []struct {
		name    string
		srcs    []source
		want    []string
		notWant []string
	}{
		{
			name: "все подключены — второй строки нет",
			srcs: []source{
				{flag: "analytics-config", path: "/k/analytics_config.json"},
				{flag: "ledger", path: "/k/transactions.jsonl"},
			},
			want:    []string{"kbengine: sources connected: --analytics-config, --ledger"},
			notWant: []string{"not connected"},
		},
		{
			name: "ни одного — только строка о неподключённых",
			srcs: []source{
				{flag: "analytics-config"},
				{flag: "ledger"},
			},
			want: []string{
				"kbengine: sources not connected: --analytics-config, --ledger",
			},
			notWant: []string{"sources connected:"},
		},
		{
			name: "смешанно — обе строки, каждый флаг ровно в одной",
			srcs: []source{
				{flag: "analytics-config", path: "/k/analytics_config.json"},
				{flag: "ledger"},
				{flag: "team", path: "/k/team.json"},
				{flag: "now"},
			},
			want: []string{
				"kbengine: sources connected: --analytics-config, --team",
				"kbengine: sources not connected: --ledger, --now",
			},
		},
		{
			name: "порядок объявления сохраняется, а не сортируется",
			srcs: []source{
				{flag: "now"},
				{flag: "analytics-config"},
				{flag: "ledger"},
			},
			want: []string{"not connected: --now, --analytics-config, --ledger"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(startupSources(tt.srcs), "\n")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("отчёт не содержит %q:\n%s", w, got)
				}
			}
			for _, n := range tt.notWant {
				if strings.Contains(got, n) {
					t.Errorf("отчёт содержит лишнее %q:\n%s", n, got)
				}
			}
		})
	}
}

// Строка о неподключённом источнике должна объяснять последствие, а не только
// перечислять флаги: «--analytics-config» ничего не говорит тому, кто смотрит
// на пустую вкладку и не знает, что она читает именно этот файл.
func TestStartupSources_namesTheConsequence(t *testing.T) {
	got := strings.Join(startupSources([]source{{flag: "analytics-config"}}), "\n")
	if !strings.Contains(got, "empty") {
		t.Errorf("строка не объясняет, что вкладки останутся пустыми: %s", got)
	}
}

// syncBuffer нужен потому, что serve пишет из своей горутины, пока тест читает:
// обычный bytes.Buffer здесь ловится детектором гонок, который гоняет CI.
type syncBuffer struct {
	mu  gosync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Юнит выше проверяет, как отчёт СОБРАН; этот — что он вообще ПЕЧАТАЕТСЯ при
// запуске. Разница не теоретическая: молчал движок именно на этом шаге, а
// функция, которую никто не зовёт, проходит любые тесты.
func TestRun_serve_announcesUnconnectedSources(t *testing.T) {
	catalog := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)

	var out, errb syncBuffer
	go func() {
		run([]string{"serve", "--catalog", catalog, "--addr", "127.0.0.1:0"}, &out, &errb)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "not connected") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := out.String()
	if !strings.Contains(got, "--analytics-config") {
		t.Errorf("запуск без семантического слоя молчит о нём:\n%s", got)
	}
	if !strings.Contains(got, "serving dashboard") {
		t.Errorf("обычная строка запуска пропала:\n%s", got)
	}
}
