package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// Папки берутся из конфига окна: выбранное в kb-desktop и есть то, по чему
// ищет агент. Исключенная папка (finances) в выдачу не попадает, а снимок
// индекса ложится рядом с конфигом — тот же файл, что у окна.
func TestFileSearch_usesTheWindowConfig(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "notes")
	if err := os.MkdirAll(filepath.Join(root, "finances"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(root, "a.md"), "единорог в заметке"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(root, "finances", "b.md"), "единорог в деньгах"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "kb-desktop", "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Roots: []string{root}, Exclude: []string{"finances"}}); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	x, err := newFileIndex(cfgPath, dir, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	x.touch(now)
	x.refresh(context.Background(), now)
	hits, err := x.svc.Search("единорог", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Doc.Path() != filepath.Join(root, "a.md") {
		t.Fatalf("hits = %v, ждали только a.md", hits)
	}
	if _, err := os.Stat(desktopconfig.IndexPath(cfgPath)); err != nil {
		t.Fatalf("снимок индекса не рядом с конфигом: %v", err)
	}
}

func TestFileSearch_refusals(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone")
	cfgGone := filepath.Join(dir, "gone.json")
	if err := desktopconfig.Write(cfgGone, desktopconfig.Config{Roots: []string{gone}}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		// Конфига нет — окно ни разу не запускалось. MCP умолчаний не
		// выдумывает и сам файл не создает: папки выбирает человек в окне.
		{"конфига нет", filepath.Join(dir, "none.json"), "kb-desktop"},
		// Ни одной доступной папки — отказ, а не пустой индекс: иначе агент
		// получал бы «ничего не найдено» по любому запросу.
		{"все папки пропали", cfgGone, "нет ни одной"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, err := newFileIndex(tt.path, dir, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, ждали со словом %q", err, tt.want)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "none.json")); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatal("конфиг создан читателем")
			}
		})
	}
}

// Пропавшая папка рядом с живой не роняет сервер, но называется вслух в
// stderr — тем же текстом, что у окна.
func TestFileSearch_namesSkippedRoots(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := desktopconfig.Write(cfgPath, desktopconfig.Config{Roots: []string{dir, filepath.Join(dir, "gone")}}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if _, err := newFileIndex(cfgPath, dir, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "gone") {
		t.Fatalf("stderr молчит о пропавшей папке: %q", stderr.String())
	}
}

// mustWrite кладет файл, создавая папки.
func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
}

// saveConfig пишет конфиг, как окно, и сдвигает время изменения: на быстрой
// машине две записи подряд могут совпасть по mtime.
func saveConfig(t *testing.T, path string, cfg desktopconfig.Config, at time.Time) {
	t.Helper()
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// Папка, закрытая в окне, закрывается и для агента — без перезапуска MCP.
// Прежде конфиг читался один раз при старте, и убранная в окне личная папка
// оставалась доступной read_file до конца сессии (ревью #375).
func TestFileIndex_followsTheWindowConfig(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	mustWrite(t, filepath.Join(a, "one.md"), "единорог раз")
	mustWrite(t, filepath.Join(a, "private", "p.md"), "единорог личный")
	mustWrite(t, filepath.Join(b, "two.md"), "единорог два")
	start := time.Now()

	tests := []struct {
		name     string
		next     desktopconfig.Config
		refused  string // файл, который после смены конфига не читается
		readable string // файл, который читается
	}{
		{"папку сменили", desktopconfig.Config{Roots: []string{b}}, filepath.Join(a, "one.md"), filepath.Join(b, "two.md")},
		{"папку исключили", desktopconfig.Config{Roots: []string{a}, Exclude: []string{"private"}}, filepath.Join(a, "private", "p.md"), filepath.Join(a, "one.md")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			saveConfig(t, cfgPath, desktopconfig.Config{Roots: []string{a}}, start.Add(-time.Hour))
			var stderr bytes.Buffer
			x, err := newFileIndex(cfgPath, dir, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			x.touch(start) // сессия ищет: индекс держится и после смены папок
			x.refresh(ctx, start)
			if _, err := x.svc.Read(tt.refused); err != nil {
				t.Fatalf("до смены конфига файл должен читаться: %v", err)
			}

			saveConfig(t, cfgPath, tt.next, start)
			x.refresh(ctx, start.Add(configPollEvery))
			if _, err := x.svc.Read(tt.refused); !errors.Is(err, filesearch.ErrNotAllowed) {
				t.Fatalf("после смены конфига %s: err = %v, ждали ErrNotAllowed", tt.refused, err)
			}
			if _, err := x.svc.Read(tt.readable); err != nil {
				t.Fatalf("%s не читается: %v", tt.readable, err)
			}
			hits, err := x.svc.Search("единорог", 10)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range hits {
				if h.Doc.Path() == tt.refused {
					t.Fatalf("закрытый файл в выдаче: %s", h.Doc.Path())
				}
			}
		})
	}
}

// Индекс догоняет диск по часам: до срока новый файл не ищется (сборка не
// дергается на каждый опрос конфига), после срока — находится.
func TestFileIndex_rebuildsOnSchedule(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "notes")
	mustWrite(t, filepath.Join(root, "old.md"), "старое")
	cfgPath := filepath.Join(dir, "config.json")
	saveConfig(t, cfgPath, desktopconfig.Config{Roots: []string{root}}, time.Now().Add(-time.Hour))
	var stderr bytes.Buffer
	x, err := newFileIndex(cfgPath, dir, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, now := context.Background(), time.Now()
	x.touch(now)
	x.refresh(ctx, now)
	mustWrite(t, filepath.Join(root, "new.md"), "свежая мысль")

	x.refresh(ctx, now.Add(configPollEvery))
	if hits, _ := x.svc.Search("свежая", 5); len(hits) != 0 {
		t.Fatalf("пересборка раньше срока: %v", hits)
	}
	x.refresh(ctx, now.Add(filesRebuildEvery))
	if hits, _ := x.svc.Search("свежая", 5); len(hits) != 1 {
		t.Fatalf("после срока новый файл не найден: %v", hits)
	}
}

// «~» в args MCP-конфига shell не раскрывает: путь из JSON приходит как есть,
// и без раскрытия сервер советовал бы запустить окно, которое уже запущено.
func TestFileIndex_expandsTildeInConfigPath(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "notes")
	mustWrite(t, filepath.Join(root, "a.md"), "текст")
	saveConfig(t, filepath.Join(home, "kb", "config.json"), desktopconfig.Config{Roots: []string{root}}, time.Now())
	var stderr bytes.Buffer
	if _, err := newFileIndex("~/kb/config.json", home, &stderr); err != nil {
		t.Fatalf("тильда не раскрыта: %v", err)
	}
}

// newIndexOver поднимает индекс над одной папкой с файлом «единорог».
func newIndexOver(t *testing.T) (*fileIndex, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "notes")
	file := filepath.Join(root, "a.md")
	mustWrite(t, file, "единорог")
	// Файл старше сборки: только что записанный перечитывается всегда (racy-clean).
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.json")
	saveConfig(t, cfgPath, desktopconfig.Config{Roots: []string{root}}, time.Now().Add(-time.Hour))
	var stderr bytes.Buffer
	x, err := newFileIndex(cfgPath, dir, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	return x, file
}

// Сессия, которая не ищет по файлам, индекс не собирает и не держит: из
// шести открытых сессий ищет обычно одна, а каждая держала ~100 МБ (замер
// 03.11). Чтение файла индекса не требует и работает сразу.
func TestFileIndex_noBuildUntilSearched(t *testing.T) {
	x, file := newIndexOver(t)
	ctx, now := context.Background(), time.Now()
	x.refresh(ctx, now)
	x.refresh(ctx, now.Add(filesRebuildEvery))
	if st := x.svc.Stats(); st.Indexed != 0 {
		t.Fatalf("индекс собран без поиска: %+v", st)
	}
	if _, err := os.Stat(desktopconfig.IndexPath(x.cfgPath)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("снимок записан без поиска: %v", err)
	}
	if _, err := x.Read(file); err != nil {
		t.Fatalf("read_file без индекса: %v", err)
	}
}

// Первый search_files дожидается сборки, а не отвечает «еще собирается»:
// со снимка она идет меньше секунды.
func TestFileIndex_firstSearchWaitsForTheBuild(t *testing.T) {
	x, file := newIndexOver(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { x.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	hits, err := x.Search("единорог", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Doc.Path() != file {
		t.Fatalf("hits = %v", hits)
	}
}

// Долго не искавшая сессия отпускает индекс; следующий поиск поднимает его
// заново из снимка. Чтение при этом не закрывается.
func TestFileIndex_unloadsWhenIdle(t *testing.T) {
	x, file := newIndexOver(t)
	ctx, now := context.Background(), time.Now()
	x.touch(now)
	x.refresh(ctx, now)
	if st := x.svc.Stats(); st.Indexed != 1 {
		t.Fatalf("после поиска индекс не собран: %+v", st)
	}

	x.refresh(ctx, now.Add(filesIdleAfter-configPollEvery))
	if st := x.svc.Stats(); st.Indexed != 1 {
		t.Fatalf("выгружен раньше срока: %+v", st)
	}
	x.refresh(ctx, now.Add(filesIdleAfter))
	if _, err := x.svc.Search("единорог", 5); !errors.Is(err, filesearch.ErrNotBuilt) {
		t.Fatalf("после простоя: err = %v, ждали выгруженный индекс", err)
	}
	if _, err := x.Read(file); err != nil {
		t.Fatalf("read_file после выгрузки: %v", err)
	}

	later := now.Add(filesIdleAfter + time.Minute)
	x.touch(later)
	x.refresh(ctx, later)
	if st := x.svc.Stats(); st.Indexed != 1 || st.Reused != 1 {
		t.Fatalf("повторный поиск: %+v, ждали индекс из снимка", st)
	}
}

// Папка, закрытая в окне, пока сессия не ищет, закрывается для read_file
// так же, а индекс после смены папок в памяти не остается.
func TestFileIndex_configChangeWhileIdleStillCloses(t *testing.T) {
	x, file := newIndexOver(t)
	other := filepath.Join(filepath.Dir(x.cfgPath), "other")
	mustWrite(t, filepath.Join(other, "b.md"), "другое")
	ctx, now := context.Background(), time.Now()
	saveConfig(t, x.cfgPath, desktopconfig.Config{Roots: []string{other}}, now)
	x.refresh(ctx, now)
	if _, err := x.Read(file); !errors.Is(err, filesearch.ErrNotAllowed) {
		t.Fatalf("закрытая папка: err = %v, ждали ErrNotAllowed", err)
	}
	// Сессия без поиска ради смены папок диск не обходит: прежде каждая из
	// простаивающих собирала индекс целиком и только потом отпускала (ревью #377).
	if _, err := os.Stat(desktopconfig.IndexPath(x.cfgPath)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("смена конфига без поиска собрала индекс: %v", err)
	}
	if _, err := x.svc.Search("другое", 5); !errors.Is(err, filesearch.ErrNotBuilt) {
		t.Fatalf("индекс в памяти сессии без поиска: err = %v", err)
	}
	if _, err := x.Read(filepath.Join(other, "b.md")); err != nil {
		t.Fatalf("новая папка не читается: %v", err)
	}
}

// Поиск, пришедший между проверкой простоя и выгрузкой, видит loaded, а
// индекса уже нет. Он не отвечает «еще собирается», а будит сборку (ревью #377).
func TestFileIndex_searchAfterUnloadRaceStillAnswers(t *testing.T) {
	x, file := newIndexOver(t)
	x.loaded.Store(true) // состояние гонки: флаг еще стоит, индекса нет
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { x.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	hits, err := x.Search("единорог", 5)
	if err != nil || len(hits) != 1 || hits[0].Doc.Path() != file {
		t.Fatalf("hits = %v, err = %v", hits, err)
	}
}

// После остановки run поиск не ждет searchWaitMax впустую: будить некого.
func TestFileIndex_searchAfterStopDoesNotHang(t *testing.T) {
	x, _ := newIndexOver(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { x.run(ctx); close(done) }()
	cancel()
	<-done

	start := time.Now()
	if _, err := x.Search("единорог", 5); !errors.Is(err, filesearch.ErrNotBuilt) {
		t.Fatalf("err = %v, ждали ErrNotBuilt", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("поиск после остановки ждал %s", d)
	}
}

// Упавшая сборка повторяется не чаще опроса конфига: иначе каждый
// search_files запускал бы новый полный обход (ревью #377).
func TestFileIndex_failedBuildRetriesAtPollPace(t *testing.T) {
	x, _ := newIndexOver(t)
	now := time.Now()
	x.touch(now)
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	x.refresh(dead, now) // сборка падает: обход отменен
	if x.loaded.Load() {
		t.Fatal("упавшая сборка отмечена как удачная")
	}

	ctx := context.Background()
	x.touch(now.Add(time.Second))
	x.refresh(ctx, now.Add(time.Second))
	if x.loaded.Load() {
		t.Fatal("повтор сборки раньше опроса конфига")
	}
	x.refresh(ctx, now.Add(configPollEvery))
	if !x.loaded.Load() {
		t.Fatal("после паузы сборка не повторена")
	}
}
