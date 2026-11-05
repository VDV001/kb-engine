package financejsonl_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Журнал в хранилище читается и пишется тем же кодом, что файл: Parse и Encode
// дают ровно то, что Load и Save.
func TestParseEncodeMatchLoadSave(t *testing.T) {
	recs := []finance.Record{expense(t, "01A"), expense(t, "01B")}
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	if err := financejsonl.Save(path, recs, loadedAt); err != nil {
		t.Fatal(err)
	}
	fromFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var enc bytes.Buffer
	if err := financejsonl.Encode(&enc, recs); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(fromFile, enc.Bytes()) {
		t.Fatalf("Encode differs from Save:\nfile %s\nenc  %s", fromFile, enc.Bytes())
	}
	got, err := financejsonl.Parse("users/u1/fin/journal.jsonl", bytes.NewReader(enc.Bytes()), loadedAt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	loaded, err := financejsonl.Load(path, loadedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(loaded) || got[0].Transaction().ID() != loaded[0].Transaction().ID() {
		t.Fatalf("Parse = %d records, Load = %d", len(got), len(loaded))
	}
}

// Ошибка разбора называет то имя, что дал вызывающий: ключ хранилища, а не
// имя файла, которого нет.
func TestParseNamesTheSourceAndRefusesDuplicates(t *testing.T) {
	bad := `{"id":"01A","kind":"expense"` + "\n"
	_, err := financejsonl.Parse("users/u1/fin/journal.jsonl", strings.NewReader(bad), loadedAt)
	if err == nil || !strings.Contains(err.Error(), "users/u1/fin/journal.jsonl line 1") {
		t.Fatalf("err = %v, want the key and the line", err)
	}
	var enc bytes.Buffer
	if err := financejsonl.Encode(&enc, []finance.Record{expense(t, "01A")}); err != nil {
		t.Fatal(err)
	}
	twice := enc.String() + enc.String()
	if _, err := financejsonl.Parse("j", strings.NewReader(twice), loadedAt); !errors.Is(err, finance.ErrDuplicateID) {
		t.Fatalf("duplicate id: err = %v, want ErrDuplicateID", err)
	}
	empty, err := financejsonl.Parse("j", strings.NewReader(""), loadedAt)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty journal: %d, %v", len(empty), err)
	}
}
