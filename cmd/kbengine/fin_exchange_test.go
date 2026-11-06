package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Обмен валюты одной командой на Маке (#432): --received и --received-account
// — вторая сторона; курс не вводится, а считается из двух сумм и печатается.
// Без --cat обмен записывается переводом себе («Прочее» / «Перевод себе»):
// обмен — не трата. Суммы и счета выдуманные (гейт owner-data).
func TestRun_finAdd_exchange(t *testing.T) {
	cases := []struct {
		name     string
		extra    []string
		wantCode int
		wantLine []string // substrings of the written line
		wantOut  string   // substring of stdout
		wantErr  string   // substring of stderr, lower case
	}{
		{
			name:     "рубли на доллары: валюта получения — у счета из книги",
			extra:    []string{"--account", "Сбербанк", "--amount", "9000", "--received", "100", "--received-account", "Банк USD"},
			wantLine: []string{`"category":"Прочее"`, `"subcategory":"Перевод себе"`, `"received":{"amount":"100.00","currency":"USD","account":"Банк USD"}`},
			wantOut:  "курс 90.00",
		},
		{
			name:     "сумма получения без счета — отказ",
			extra:    []string{"--account", "Сбербанк", "--amount", "9000", "--received", "100"},
			wantCode: 2,
			wantErr:  "--received-account",
		},
		{
			name:     "счет получения без суммы — отказ",
			extra:    []string{"--account", "Сбербанк", "--amount", "9000", "--received-account", "Банк USD"},
			wantCode: 2,
			wantErr:  "--received",
		},
		{
			name:     "на тот же счет — отказ",
			extra:    []string{"--account", "Сбербанк", "--amount", "9000", "--received", "9000", "--received-account", "Сбербанк"},
			wantCode: 1,
			wantErr:  "same account",
		},
		{
			name:     "рубли на долларовый счет — отказ",
			extra:    []string{"--account", "Сбербанк", "--amount", "9000", "--received", "9000", "--received-currency", "RUB", "--received-account", "Банк USD"},
			wantCode: 1,
			wantErr:  "currency",
		},
		{
			name:     "обмен — не трата: с категорией траты отказ",
			extra:    []string{"--account", "Сбербанк", "--amount", "9000", "--cat", "Еда", "--received", "100", "--received-account", "Банк USD"},
			wantCode: 1,
			wantErr:  "transfer to self",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			book := workbookWithDollarAccount(t)
			ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
			finImport(t, book, ledger)

			args := append([]string{"fin", "add", "--ledger", ledger, "--from", book, "--date", "2026-04-06"}, tc.extra...)
			var out, errb bytes.Buffer
			code := run(args, &out, &errb)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, tc.wantCode, errb.String())
			}
			if tc.wantCode != 0 {
				if !strings.Contains(strings.ToLower(errb.String()), tc.wantErr) {
					t.Errorf("stderr = %q, want it to mention %q", errb.String(), tc.wantErr)
				}
				return
			}
			line := lastLedgerLine(t, ledger)
			for _, w := range tc.wantLine {
				if !strings.Contains(line, w) {
					t.Errorf("строка журнала без %s:\n%s", w, line)
				}
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("stdout = %q, want %q", out.String(), tc.wantOut)
			}
		})
	}
}

// fin report называет обмен отдельной строкой с курсом записи, не в тратах.
func TestRun_finReport_listsAnExchange(t *testing.T) {
	book := workbookWithDollarAccount(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, book, ledger)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--from", book, "--date", "2026-04-06",
		"--account", "Сбербанк", "--amount", "9000", "--received", "100", "--received-account", "Банк USD"}, &out, &errb); code != 0 {
		t.Fatalf("fin add exit = %d, stderr = %s", code, errb.String())
	}
	out.Reset()
	if code := run([]string{"fin", "report", "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin report exit = %d, stderr = %s", code, errb.String())
	}
	for _, want := range []string{"обмен", "2026-04-06", "Сбербанк", "100.00 USD", "Банк USD", "курс 90.00"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("отчет без %q:\n%s", want, out.String())
		}
	}
}
