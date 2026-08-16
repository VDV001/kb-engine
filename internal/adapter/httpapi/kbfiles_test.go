package httpapi_test

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

// Собственные артефакты базы — шпаргалки, курсы, разборы, страницы проектов —
// до сих пор существовали строкой в каталоге и не открывались из витрины
// ничем: маршрута, отдающего их, у движка не было вовсе. Замер на живом
// каталоге: 104 записи несут file и не несут url, то есть открыть их можно
// было только через file:// мимо базы.
//
// Маршрут отдаёт файл ТОЛЬКО если его путь стоит в поле file какой-нибудь
// записи каталога. Это не файловый сервер, а список разрешённого, и разница
// принципиальная: рядом с каталогом лежат личные заметки и финансы, и они
// недостижимы даже по точному пути, потому что каталог их не называет.
func kbFS() fstest.MapFS {
	return fstest.MapFS{
		// Путь стоит в поле file записи 2 фальшивого каталога.
		"notes/2026-08-02_hello.md": {Data: []byte("# Разбор\n")},
		// Файл существует, но каталог его не называет.
		"notes/personal.md": {Data: []byte("не для витрины\n")},
	}
}

func kbServer(fsys fstest.MapFS) http.Handler {
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) {
			return changelog.Document{CurrentVersion: "0.9.0"}, nil
		},
		httpapi.Documents{Artefacts: fsys}, testEngine, nil)
}

func TestKBFiles_servesPathNamedByCatalog(t *testing.T) {
	rec := get(t, kbServer(kbFS()), "/kb/notes/2026-08-02_hello.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "# Разбор\n" {
		t.Fatalf("body = %q, want the artefact", got)
	}
}

// Главное правило маршрута. Файл на диске есть, каталог о нём не знает —
// значит для витрины его не существует. Без этой проверки маршрут был бы
// обычным файловым сервером поверх личного дерева.
func TestKBFiles_refusesPathTheCatalogDoesNotName(t *testing.T) {
	rec := get(t, kbServer(kbFS()), "/kb/notes/personal.md")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — каталог этот путь не называет", rec.Code)
	}
}

// Обход вверх по дереву невозможен не потому, что путь чистится, а потому что
// сравнение идёт со списком: "../.." в списке не стоит и стоять не может.
func TestKBFiles_refusesTraversal(t *testing.T) {
	for _, p := range []string{
		"/kb/../finances/transactions.jsonl",
		"/kb/notes/../../etc/passwd",
	} {
		rec := get(t, kbServer(kbFS()), p)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s: status 200 — путь наружу отдался", p)
		}
	}
}

// Путь каталог называет, а файла нет: это про записи, чей артефакт
// переименовали или не написали. Ответ 404, а не 500 — сервер исправен.
func TestKBFiles_missingFileIsNotFound(t *testing.T) {
	rec := get(t, kbServer(fstest.MapFS{}), "/kb/notes/2026-08-02_hello.md")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// Источник не подключён — маршрут обязан молчать 404, а не падать.
func TestKBFiles_withoutArtefactsSourceIsNotFound(t *testing.T) {
	rec := get(t, newTestServer(), "/kb/notes/2026-08-02_hello.md")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
