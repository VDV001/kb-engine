package catalogjson_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/filebackup"
)

// Каталог намеренно не под git — рядом лежат личные файлы владельца. Значит
// снимок рядом с файлом это ЕДИНСТВЕННЫЙ механизм отката, и до сих пор его
// имели только деньги: журнал и книга. Ошибочная миграция по всей базе
// восстановлению не подлежала.
//
// Атомарная запись от этого не защищает: она бережёт от обрывка файла, а не от
// неверного содержимого, записанного целиком и успешно.
//
// Перечислены ОБЯЗАННЫЕ, а не соблюдающие: каждый публичный писатель каталога
// стоит в таблице строкой. Новый писатель, добавленный мимо неё, тестом не
// проверяется — на это отвечает второй тест ниже, считающий писателей в коде.
func TestCatalogWriters_leaveASnapshot(t *testing.T) {
	const doc = `{"entries":[{"id":1,"title":"T","url":"https://h/x","category":"golang","status":"keep","lifecycle":"active","habr_id":"1030928"}]}`

	cases := []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"SetFields", func(t *testing.T, path string) {
			if _, err := catalogjson.SetFields(path, []int{1}, catalogjson.Changes{Lifecycle: "outdated"}); err != nil {
				t.Fatalf("SetFields: %v", err)
			}
		}},
		{"ApplyDrift", func(t *testing.T, path string) {
			if _, err := catalogjson.ApplyDrift(path, []catalogjson.DriftRecord{{EntryID: 1, Code: 404}}); err != nil {
				t.Fatalf("ApplyDrift: %v", err)
			}
		}},
		{"MigrateHabrIDs", func(t *testing.T, path string) {
			if _, err := catalogjson.MigrateHabrIDs(path, true); err != nil {
				t.Fatalf("MigrateHabrIDs: %v", err)
			}
		}},
		{"MigrateURLs", func(t *testing.T, path string) {
			if _, err := catalogjson.MigrateURLs(path, true); err != nil {
				t.Fatalf("MigrateURLs: %v", err)
			}
		}},
		{"MigrateVersions", func(t *testing.T, path string) {
			if _, err := catalogjson.MigrateVersions(path, true); err != nil {
				t.Fatalf("MigrateVersions: %v", err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "catalog.json")
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatalf("write catalog: %v", err)
			}

			tc.write(t, path)

			names := snapshots(t, dir)
			if len(names) == 0 {
				t.Fatalf("%s переписал каталог, не оставив снимка", tc.name)
			}
			// Снимок обязан хранить состояние ДО правки — иначе он копия того,
			// что уже записано, и откатывать по нему нечего.
			before, err := os.ReadFile(filepath.Join(dir, filebackup.DirName, names[0]))
			if err != nil {
				t.Fatalf("read snapshot: %v", err)
			}
			if strings.TrimSpace(string(before)) != doc {
				t.Errorf("снимок снят после правки, а не до неё:\n%s", before)
			}
		})
	}
}

// Обратная сторона: тест выше проверяет перечисленных, а прикрыть надо всех.
// Единственная точка записи — writeFileAtomic; если появится второй путь,
// снимок обойдут молча.
func TestCatalogHasOneWritePath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// os.WriteFile и os.Rename мимо writeFileAtomic — это второй путь
		// записи, у которого своего снимка нет.
		for _, marker := range []string{"os.WriteFile(", "os.Rename("} {
			if strings.Contains(string(src), marker) && name != "write.go" {
				offenders = append(offenders, name+": "+marker)
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("запись каталога мимо writeFileAtomic (снимка у неё нет): %v", offenders)
	}
}

func snapshots(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, filebackup.DirName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read backup dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
