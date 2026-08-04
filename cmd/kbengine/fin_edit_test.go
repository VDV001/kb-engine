package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Правка записи через движок, а не руками в файле: ровно ручная правка однажды
// положила в книгу строку без id, и синк принял её как новую.

func finEdit(t *testing.T, ledger string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"fin", "edit", "--ledger", ledger}, extra...)
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// rowOf находит строку леджера по id и разбирает её как JSON. Проверяется файл,
// а не собственный парсер: именно в файл смотрит следующая команда.
func rowOf(t *testing.T, ledger, id string) map[string]any {
	t.Helper()
	for _, line := range readLedgerFile(t, ledger) {
		if !strings.Contains(line, id) {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("строка леджера не разбирается: %v", err)
		}
		return row
	}
	t.Fatalf("запись %s не найдена в леджере", id)
	return nil
}

// lastID возвращает идентификатор последней строки файла.
func lastID(t *testing.T, ledger string) string {
	t.Helper()
	lines := readLedgerFile(t, ledger)
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &row); err != nil {
		t.Fatalf("строка леджера не разбирается: %v", err)
	}
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatal("у последней строки нет id")
	}
	return id
}

func TestRun_finEdit_setsTheAccount(t *testing.T) {
	_, ledger := pairedLedger(t)
	addToLedgerWithAccount(t, ledger, "")
	id := lastID(t, ledger)

	code, stdout, stderr := finEdit(t, ledger, "--id", id, "--account", "Сбербанк")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "Сбербанк") {
		t.Errorf("вывод не подтверждает правку: %q", stdout)
	}

	row := rowOf(t, ledger, id)
	if got := row["account"]; got != "Сбербанк" {
		t.Errorf("счёт в леджере = %v, ожидался Сбербанк", got)
	}
	// Правка одного поля не должна уносить остальные.
	if got := row["description"]; got != "такси до центра" {
		t.Errorf("заметка потеряна: %v", got)
	}
	if got := row["amount"]; got != "322.00" {
		t.Errorf("сумма изменилась: %v", got)
	}
	if rev, _ := row["rev"].(float64); rev < 2 {
		t.Errorf("ревизия = %v, правка должна её повысить", row["rev"])
	}
}

// Неизвестный id — это опечатка, и она должна быть названа, а не проглочена
// молча с нулевым кодом возврата.
func TestRun_finEdit_refusesUnknownID(t *testing.T) {
	_, ledger := pairedLedger(t)

	code, _, stderr := finEdit(t, ledger, "--id", "01NOSUCHID", "--account", "Сбербанк")
	if code == 0 {
		t.Error("правка несуществующей записи завершилась успехом")
	}
	if !strings.Contains(stderr, "01NOSUCHID") {
		t.Errorf("в отказе не назван id: %q", stderr)
	}
}

// Правка без единого поля — почти наверняка забытый флаг.
func TestRun_finEdit_refusesEmptyChange(t *testing.T) {
	_, ledger := pairedLedger(t)
	addToLedgerWithAccount(t, ledger, "Сбербанк")
	id := lastID(t, ledger)

	code, _, stderr := finEdit(t, ledger, "--id", id)
	if code == 0 {
		t.Error("пустая правка принята")
	}
	if stderr == "" {
		t.Error("пустая правка отвергнута молча")
	}
}

// Стирание выражается явно: --account= отличается от «флаг не передавали».
func TestRun_finEdit_clearsOnExplicitEmpty(t *testing.T) {
	_, ledger := pairedLedger(t)
	addToLedgerWithAccount(t, ledger, "Сбербанк")
	id := lastID(t, ledger)

	code, _, stderr := finEdit(t, ledger, "--id", id, "--account=")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if got := rowOf(t, ledger, id)["account"]; got != nil && got != "" {
		t.Errorf("счёт не стёрт: %v", got)
	}
}
