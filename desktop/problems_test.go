package main

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/extractcache"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
)

// Панель «Проблемы» (#456): беды всех источников — в одной модели, у каждой
// источник, что не так, что сделать и исходная ошибка.
func TestCollectProblems(t *testing.T) {
	work := desktopconfig.Source{ID: "f1", Kind: desktopconfig.KindFolder, Name: "Работа", Path: "~/work", Enabled: true}
	net := desktopconfig.Source{ID: "f2", Kind: desktopconfig.KindFolder, Path: "/Volumes/share", Enabled: true}
	cat := desktopconfig.Source{ID: "c1", Kind: desktopconfig.KindCatalog, Path: "~/kb/catalog.json", Enabled: true}
	srv := desktopconfig.Source{ID: "s1", Kind: desktopconfig.KindServer, URL: "https://kb.example", Enabled: true}
	off := desktopconfig.Source{ID: "s2", Kind: desktopconfig.KindServer, URL: "https://off.example"}
	rows := []sourceRow{
		{Source: work, State: stateOK},
		{Source: net, State: stateMissing, Note: "/Volumes/share: нет такой папки"},
		{Source: cat, State: stateMissing, Note: "open catalog.json: no such file"},
		{Source: srv, State: stateMissing, Note: kbserver.Describe(kbserver.ErrUnauthorized)},
		{Source: off, State: stateOff},
	}
	perm := fmt.Errorf("open: %w", fs.ErrPermission)
	st := indexStatus{
		Skipped: 3, // список несет первые 100, счет — все
		SkippedFiles: []skippedRow{
			{Path: "/Users/me/work/a.docx", Reason: "это веб-страница — скачайте заново", Detail: "web page"},
			{Path: "/Users/me/work/b.md", Reason: "нет прав на чтение — дайте окну доступ", Detail: perm.Error()},
		},
		Unrecognized: []skippedRow{{Path: "/Users/me/work/scan.pdf", Reason: recognizeFailed, Detail: "boom"}},
	}
	got := collectProblems(rows, st, "/Users/me")

	if got.Total != 7 { // папка, каталог, сервер, 3 нечитаемых (счет, а не список), 1 нераспознанный
		t.Errorf("Total = %d, want 7: %+v", got.Total, got.Problems)
	}
	bySource := map[string][]problem{}
	for _, p := range got.Problems {
		bySource[p.Source] = append(bySource[p.Source], p)
		if p.What == "" {
			t.Errorf("проблема без «что не так»: %+v", p)
		}
	}
	if len(bySource["s2"]) != 0 || len(bySource["f1"]) != 3 {
		t.Fatalf("по источникам: %+v", bySource)
	}
	if p := bySource["s1"]; len(p) != 1 || !strings.Contains(p[0].What, "401") || p[0].Todo == "" || p[0].Name != "kb.example" {
		t.Errorf("сервер: %+v", p)
	}
	if p := bySource["f2"]; len(p) != 1 || p[0].Todo == "" || p[0].Detail != "/Volumes/share: нет такой папки" || p[0].Name != "share" {
		t.Errorf("папка: %+v", p)
	}
	if p := bySource["c1"]; len(p) != 1 || p[0].Detail == "" || p[0].Todo == "" {
		t.Errorf("каталог: %+v", p)
	}
	for _, p := range bySource["f1"] {
		if p.Path == "" || p.Name != "Работа" {
			t.Errorf("файл папки «Работа»: %+v", p)
		}
	}
	if p := bySource["f1"][0]; p.What != "это веб-страница" || p.Todo != "скачайте заново" {
		t.Errorf("причина и действие разделены: %+v", p)
	}
}

// Индекс не собрался — проблема всех папок сразу, не одного файла.
func TestCollectProblems_IndexError(t *testing.T) {
	got := collectProblems(nil, indexStatus{Error: "walk: boom"}, "/Users/me")
	if got.Total != 1 || got.Problems[0].Detail != "walk: boom" || got.Problems[0].Todo == "" {
		t.Fatalf("got %+v", got)
	}
}

// Файл вне известных папок (папку убрали, а сборка старая) не теряется:
// он в общей группе папок.
func TestCollectProblems_FileOutsideFolders(t *testing.T) {
	st := indexStatus{Skipped: 1, SkippedFiles: []skippedRow{{Path: "/elsewhere/x.md", Reason: "не читается: boom", Detail: "boom"}}}
	got := collectProblems(nil, st, "/Users/me")
	if got.Total != 1 || len(got.Problems) != 1 || got.Problems[0].Source != "" {
		t.Fatalf("got %+v", got)
	}
}

// Каждая причина, которую окно показывает, делится на «что» и «что сделать»:
// без действия проблема — снова число без пользы.
func TestReasonsNameAnAction(t *testing.T) {
	for _, err := range []error{
		kbserver.ErrForbidden, kbserver.ErrUnauthorized, kbserver.ErrNotKB, kbserver.ErrRedirect,
		kbserver.ErrTooLarge, kbserver.ErrServerError, errors.New("dial tcp: timeout"),
	} {
		if _, todo := splitReason(kbserver.Describe(err)); todo == "" {
			t.Errorf("Describe(%v) = %q: нет действия после « — »", err, kbserver.Describe(err))
		}
	}
	for _, s := range []string{keychainNote, recognizeFailed, recognizeTimedOut} {
		if _, todo := splitReason(s); todo == "" {
			t.Errorf("%q: нет действия после « — »", s)
		}
	}
}

// Сбой распознавания виден в статусе с причиной, таймаут — своей причиной;
// файл, распознанный потом, из списка уходит.
func TestIndexer_RecordsUnrecognized(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "broken.pdf"), "broken")
	ext := &fakeExtractor{err: errors.New("boom")}
	_, x, err := newAPI(desktopconfig.Config{Roots: []string{root}}, "",
		extraction{cache: extractcache.Cache{Dir: t.TempDir()}, helper: ext})
	if err != nil {
		t.Fatal(err)
	}
	x.start(t.Context())
	waitSettled(t, x)
	u := x.Status().Unrecognized
	if len(u) != 1 || filepath.Base(u[0].Path) != "broken.pdf" || u[0].Detail != "boom" || u[0].Reason != recognizeFailed {
		t.Fatalf("Unrecognized = %+v", u)
	}

	x.noteRecognized(u[0].Path, nil)
	if u := x.Status().Unrecognized; len(u) != 0 {
		t.Fatalf("распознанный файл остался в списке: %+v", u)
	}
	x.noteRecognized("/w/slow.pdf", fmt.Errorf("kb-extract: %w", extractcache.ErrTimeout))
	if u := x.Status().Unrecognized; len(u) != 1 || u[0].Reason != recognizeTimedOut {
		t.Fatalf("таймаут без своей причины: %+v", u)
	}
}
