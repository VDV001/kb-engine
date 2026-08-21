package searchsyn_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/searchsyn"
)

// Файл словаря живёт у владельца базы и правится руками, поэтому обе формы
// записи обязаны читаться: короткая (список равнозначных написаний) и полная
// (тема отдельно от того, что в неё входит). Ломать существующий файл ради
// нового поля значило бы, что поиск перестанет переводить термины у всех, кто
// не переписал словарь в ту же минуту.
func TestLoad_bothForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, searchsyn.FileName)
	write(t, path, `{
	  "конкурентность": ["concurrency", "горутины"],
	  "кеширование": {"same": ["caching", "кэш"], "includes": ["redis"]}
	}`)

	d, err := searchsyn.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := d["конкурентность"].Same; !slices.Equal(got, []string{"concurrency", "горутины"}) {
		t.Errorf("короткая форма → Same = %v", got)
	}
	if got := d["конкурентность"].Includes; len(got) != 0 {
		t.Errorf("короткая форма не должна давать Includes, дала %v", got)
	}
	if got := d["кеширование"].Same; !slices.Equal(got, []string{"caching", "кэш"}) {
		t.Errorf("полная форма → Same = %v", got)
	}
	if got := d["кеширование"].Includes; !slices.Equal(got, []string{"redis"}) {
		t.Errorf("полная форма → Includes = %v", got)
	}
}

// Разобранный наполовину словарь хуже отсутствующего: часть терминов молча
// перестала бы переводиться, и заметить это можно было бы только промахом
// поиска.
func TestLoad_brokenEntryIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, searchsyn.FileName)
	write(t, path, `{"кеширование": "redis"}`)

	if _, err := searchsyn.Load(path); err == nil {
		t.Fatal("строка вместо списка или объекта обязана быть ошибкой")
	}
}

func TestLoad_missingFileIsItsOwnAnswer(t *testing.T) {
	_, err := searchsyn.Load(filepath.Join(t.TempDir(), searchsyn.FileName))
	if !errors.Is(err, searchsyn.ErrNoDictionary) {
		t.Fatalf("отсутствие файла = ErrNoDictionary, получено %v", err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
