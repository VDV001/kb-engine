package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// workbookWithDollarAccount is the CLI fixture plus one account held in USD —
// the shape of the dollar accounts in the live book.
func workbookWithDollarAccount(t *testing.T) string {
	t.Helper()
	path := workbook(t)
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for cell, v := range map[string]any{
		"A4": "Банк USD", "B4": 25.5, "C4": time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC), "D4": "USD",
	} {
		if err := f.SetCellValue("Счета", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_finAdd_currency(t *testing.T) {
	cases := []struct {
		name     string
		extra    func(book string) []string
		wantCode int
		wantLine string // substring of the written line; "" = must carry no currency
		wantErr  string
	}{
		{
			name:     "валюта берётся у счёта из книги",
			extra:    func(book string) []string { return []string{"--from", book, "--account", "Банк USD"} },
			wantLine: `"currency":"USD"`,
		},
		{
			name:     "названная валюта без книги",
			extra:    func(string) []string { return []string{"--currency", "usd"} },
			wantLine: `"currency":"USD"`,
		},
		{
			name: "рубли с долларового счёта — отказ",
			extra: func(book string) []string {
				return []string{"--from", book, "--account", "Банк USD", "--currency", "RUB"}
			},
			wantCode: 1,
			wantErr:  "currency",
		},
		{
			name:     "негодный код — отказ",
			extra:    func(string) []string { return []string{"--currency", "ДОЛ"} },
			wantCode: 1,
			wantErr:  "currency",
		},
		{
			name:  "рублёвая трата поля не несёт",
			extra: func(book string) []string { return []string{"--from", book, "--account", "Сбербанк"} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			book := workbookWithDollarAccount(t)
			ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
			finImport(t, book, ledger)

			args := append([]string{"fin", "add", "--ledger", ledger,
				"--amount", "12,30", "--cat", "Сервисы", "--date", "2026-04-06"}, tc.extra(book)...)
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
			if tc.wantLine == "" {
				if strings.Contains(line, `"currency"`) {
					t.Errorf("рублёвая строка несёт валюту:\n%s", line)
				}
				return
			}
			if !strings.Contains(line, tc.wantLine) {
				t.Errorf("строка без %s:\n%s", tc.wantLine, line)
			}
		})
	}
}

// fin edit меняет валюту записи — так переводится в доллары трата, записанная
// когда-то рублями по курсу.
func TestRun_finEdit_currency(t *testing.T) {
	book := workbookWithDollarAccount(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, book, ledger)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--amount", "12,30", "--cat", "Сервисы", "--date", "2026-04-06"}, &out, &errb); code != 0 {
		t.Fatalf("fin add: %s", errb.String())
	}
	id := strings.Fields(out.String())[2]

	out.Reset()
	errb.Reset()
	if code := run([]string{"fin", "edit", "--ledger", ledger, "--id", id, "--currency", "USD"}, &out, &errb); code != 0 {
		t.Fatalf("fin edit exit = %d, stderr = %s", code, errb.String())
	}
	if line := lastLedgerLine(t, ledger); !strings.Contains(line, `"currency":"USD"`) {
		t.Errorf("после правки строка без валюты:\n%s", line)
	}
}
