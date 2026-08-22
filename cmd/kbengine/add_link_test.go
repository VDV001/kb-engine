package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalogWithCategories кладёт каталог со словарём категорий — без него
// проверка категории отвечать не может и молчит.
func catalogWithCategories(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "_data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(root, "_data", "catalog.json")
	doc := `{"entries":[],"last_updated":"2026-08-22","meta":{"categories":{` +
		`"dev-practices":"Практики разработки: спецификации, коммиты",` +
		`"golang":"Go: язык, библиотеки, архитектура"}}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

// До этой правки движок не умел записать ЧУЖУЮ статью с моим разбором: `add`
// требовал --file (свой артефакт), `inbox` жёстко ставил source=bot-inbox и
// категорию по карте хабов. Поэтому разбор дайджеста шёл правкой catalog.json
// руками — мимо всех проверок движка. Тот же класс, что был в финансах, где
// запись мимо движка оставляла строку без id.
func TestAdd_writesSomeoneElsesArticleByURL(t *testing.T) {
	path := catalogWithCategories(t)
	var out, errOut bytes.Buffer

	code := run([]string{"add", "--catalog", path,
		"--title", "Группировка ошибок и анализ причин падений (RCA) с помощью ИИ",
		"--category", "dev-practices",
		"--url", "https://habr.com/ru/articles/1067540/",
		"--source", "digest", "--verdict", "keep",
		"--author", "someone", "--tags", "testing,rca",
		"--description", "Разбор падений через ИИ",
	}, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	entries := entriesOf(t, path)
	if len(entries) != 1 {
		t.Fatalf("записей = %d, want 1", len(entries))
	}
	e := entries[0]
	for field, want := range map[string]any{
		"url":      "https://habr.com/ru/articles/1067540/",
		"category": "dev-practices",
		"source":   "digest",
		"author":   "someone",
		// Одно поле status несёт вердикт, и вердикт означает «прочитано».
		"status": "keep",
	} {
		if got := e[field]; got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
	// Номер статьи лежит в адресе. Не перенести его — потерять то, что уже
	// известно; ровно это правило движок применяет к бот-инбоксу.
	if got, ok := e["habr_id"].(float64); !ok || int(got) != 1067540 {
		t.Errorf("habr_id = %v, want 1067540", e["habr_id"])
	}
	if e["file"] != nil && e["file"] != "" {
		t.Errorf("у чужой статьи не должно быть файла: %v", e["file"])
	}
}

// Без вердикта статья не прочитана: выдумать вердикт значило бы записать
// решение, которого человек не принимал.
func TestAdd_linkWithoutAVerdictIsUnread(t *testing.T) {
	path := catalogWithCategories(t)
	var out, errOut bytes.Buffer
	code := run([]string{"add", "--catalog", path, "--title", "Статья",
		"--category", "golang", "--url", "https://example.org/a"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if got := entriesOf(t, path)[0]["status"]; got != "unread" {
		t.Errorf("status = %v, want unread", got)
	}
}

// Идентичность записи — либо файл, либо адрес. Без обоих движок не знает, что
// он добавляет, и молчаливое согласие было бы хуже отказа.
func TestAdd_refusesWithoutFileOrURL(t *testing.T) {
	path := catalogWithCategories(t)
	var out, errOut bytes.Buffer
	code := run([]string{"add", "--catalog", path, "--title", "Статья",
		"--category", "golang"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "--file") || !strings.Contains(errOut.String(), "--url") {
		t.Errorf("в отказе должны быть названы оба пути: %s", errOut.String())
	}
}

// Повтор адреса — не ошибка и не успех, как и повтор файла.
func TestAdd_refusesTheSameURLTwice(t *testing.T) {
	path := catalogWithCategories(t)
	args := []string{"add", "--catalog", path, "--title", "Статья",
		"--category", "golang", "--url", "https://example.org/a"}
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("первый прогон: exit = %d, stderr = %s", code, errOut.String())
	}
	out.Reset()
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("второй прогон: exit = %d, stderr = %s", code, errOut.String())
	}
	if n := len(entriesOf(t, path)); n != 1 {
		t.Fatalf("записей = %d, want 1", n)
	}
	if !strings.Contains(out.String(), "already in the catalog") {
		t.Errorf("повтор должен быть назван вслух: %s", out.String())
	}
}

// Категория вне словаря останавливается ДО записи, и отказ называет соседа —
// иначе человек узнает о своей опечатке из аудита через две недели.
func TestAdd_refusesACategoryOutsideTheDictionary(t *testing.T) {
	path := catalogWithCategories(t)
	var out, errOut bytes.Buffer
	code := run([]string{"add", "--catalog", path, "--title", "Статья",
		"--category", "dev-practice", "--url", "https://example.org/a"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("незаявленная категория должна быть отвергнута, exit = 0")
	}
	if n := len(entriesOf(t, path)); n != 0 {
		t.Errorf("записей = %d, каталог не должен был измениться", n)
	}
	msg := errOut.String()
	if !strings.Contains(msg, "dev-practice") {
		t.Errorf("не названа отвергнутая категория: %s", msg)
	}
	// Проверять надо саму подсказку, а не наличие строки «dev-practices»: она
	// встречается и в перечне объявленных, поэтому Contains по имени зеленел
	// бы и без подсказки вовсе — это поймала подсадка, а не чтение теста.
	if !strings.Contains(msg, `похоже на "dev-practices"`) {
		t.Errorf("не названа похожая объявленная категория: %s", msg)
	}
}

// Каталог без словаря проверить нечем, и об этом надо сказать: «проверять
// нечего» и «проверка ничего не нашла» — разные ответы.
func TestAdd_saysWhenTheCategoryWentUnchecked(t *testing.T) {
	path := emptyCatalog(t)
	var out, errOut bytes.Buffer
	code := run([]string{"add", "--catalog", path, "--title", "Статья",
		"--category", "whatever", "--url", "https://example.org/a"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "не проверял") && !strings.Contains(out.String(), "not checked") {
		t.Errorf("движок обязан назвать, чего он не проверил: %s", out.String())
	}
}
