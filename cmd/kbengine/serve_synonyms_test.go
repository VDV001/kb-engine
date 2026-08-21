package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/searchsyn"
)

// Словарь синонимов лежал рядом с каталогом и читался только терминалом:
// у serve не было ни строки про него. Это не забытая настройка, а дыра в
// проводке — правило существовало, а позвать его вторая поверхность не могла.
//
// Второй случай важнее первого: отсутствие словаря обязано называться ВСЛУХ.
// Молча оставшись без слоя перевода, поиск выглядит просто плохим, и искать
// причину будут в записях, а не в отсутствующем файле.
func TestSynonymsFor(t *testing.T) {
	t.Run("словарь рядом с каталогом подхватывается молча", func(t *testing.T) {
		dir := t.TempDir()
		catalog := filepath.Join(dir, "catalog.json")
		write(t, catalog, `{"entries":[]}`)
		write(t, filepath.Join(dir, searchsyn.FileName), `{"конкурентность":["concurrency"]}`)

		var stderr bytes.Buffer
		if opt := synonymsFor(catalog, &stderr); opt == nil {
			t.Error("словарь есть — опция обязана быть")
		}
		if stderr.Len() != 0 {
			t.Errorf("рабочий словарь не повод для строки в stderr: %q", stderr.String())
		}
	})

	t.Run("отсутствие словаря названо вслух", func(t *testing.T) {
		dir := t.TempDir()
		catalog := filepath.Join(dir, "catalog.json")
		write(t, catalog, `{"entries":[]}`)

		var stderr bytes.Buffer
		synonymsFor(catalog, &stderr)
		if !strings.Contains(stderr.String(), searchsyn.FileName) {
			t.Errorf("сервер обязан назвать отсутствующий словарь, сказано: %q", stderr.String())
		}
		if !strings.Contains(stderr.String(), "не переводит") {
			t.Errorf("сказать надо, ЧЕГО поиск теперь не делает, сказано: %q", stderr.String())
		}
	})
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
