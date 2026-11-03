package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

type appliedSources struct {
	roots, exclude []string
	catalog        string
	calls          int
}

// newSourcesSettings — настройки с двумя папками и каталогом на диске; счет
// файлов и записей подставной, чтобы проверять экран, а не индекс.
func newSourcesSettings(t *testing.T) (*folderSettings, *appliedSources, string) {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"notes", "work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "catalog.json"), []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "cfg", "config.json")
	cfg, err := desktopconfig.Load(path, home)
	if err != nil {
		t.Fatal(err)
	}
	got := &appliedSources{}
	s := &folderSettings{path: path, home: home, cfg: cfg,
		apply: func(roots, exclude []string, catalog string) error {
			got.roots, got.exclude, got.catalog = roots, exclude, catalog
			got.calls++
			return nil
		},
		countFiles: func(roots []string) map[string]int {
			m := map[string]int{}
			for _, r := range roots {
				m[r] = 7
			}
			return m
		},
		countCatalog: func() (int, error) { return 42, nil },
	}
	return s, got, home
}

func src(id string, kind desktopconfig.Kind, target string, on bool) desktopconfig.Source {
	s := desktopconfig.Source{ID: id, Kind: kind, Enabled: on}
	if kind == desktopconfig.KindServer {
		s.URL = target
	} else {
		s.Path = target
	}
	return s
}

func TestSetSources_SavesOrderAndApplies(t *testing.T) {
	s, got, home := newSourcesSettings(t)
	list := []desktopconfig.Source{
		src("", desktopconfig.KindFolder, "~/work", true),
		src("catalog-1", desktopconfig.KindCatalog, filepath.Join(home, "catalog.json"), true),
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
		// Выключенная папка может не существовать: ее не открывают.
		src("", desktopconfig.KindFolder, filepath.Join(home, "gone"), false),
		src("", desktopconfig.KindServer, "https://kb.example", true),
	}
	list[0].Name = "работа"
	if err := s.SetSources(list, []string{" Finances "}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.roots, []string{filepath.Join(home, "work"), filepath.Join(home, "notes")}) ||
		got.catalog != filepath.Join(home, "catalog.json") || !slices.Equal(got.exclude, []string{"finances"}) {
		t.Fatalf("apply(%q, %q, %q)", got.roots, got.exclude, got.catalog)
	}
	saved, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []desktopconfig.Kind
	for _, x := range saved.Sources {
		if x.ID == "" {
			t.Fatalf("id не выдан: %+v", x)
		}
		kinds = append(kinds, x.Kind)
	}
	want := []desktopconfig.Kind{desktopconfig.KindFolder, desktopconfig.KindCatalog, desktopconfig.KindFolder, desktopconfig.KindFolder, desktopconfig.KindServer}
	if !slices.Equal(kinds, want) || saved.Sources[0].Name != "работа" || saved.Sources[1].ID != "catalog-1" || saved.Sources[2].ID != "folder-1" {
		t.Fatalf("saved = %+v", saved.Sources)
	}
}

func TestSetSources_Rejects(t *testing.T) {
	tests := []struct {
		name string
		list func(home string) []desktopconfig.Source
		want error
	}{
		{"включенной папки нет", func(h string) []desktopconfig.Source {
			return []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, filepath.Join(h, "gone"), true)}
		}, errBadFolder},
		{"папка внутри папки", func(h string) []desktopconfig.Source {
			return []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, h, true), src("folder-2", desktopconfig.KindFolder, filepath.Join(h, "notes"), true)}
		}, errBadFolder},
		{"ни одной включенной папки", func(h string) []desktopconfig.Source {
			return []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, filepath.Join(h, "notes"), false)}
		}, errNoFolders},
		{"файла каталога нет", func(h string) []desktopconfig.Source {
			return []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, filepath.Join(h, "notes"), true), src("catalog-1", desktopconfig.KindCatalog, filepath.Join(h, "nope.json"), true)}
		}, errBadCatalog},
		// Одна папка двумя записями попала бы в индекс и в MCP дважды.
		{"одна папка дважды", func(h string) []desktopconfig.Source {
			return []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, "~/notes", true), src("folder-2", desktopconfig.KindFolder, filepath.Join(h, "notes"), false)}
		}, errBadFolder},
		{"сервер по http в сеть", func(h string) []desktopconfig.Source {
			return []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, filepath.Join(h, "notes"), true), src("server-1", desktopconfig.KindServer, "http://10.0.0.5", true)}
		}, desktopconfig.ErrSourceTarget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, got, home := newSourcesSettings(t)
			before, _ := os.ReadFile(s.path)
			if err := s.SetSources(tt.list(home), nil); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			// Отказ ничего не меняет: ни индекс, ни конфиг.
			after, _ := os.ReadFile(s.path)
			if got.calls != 0 || string(before) != string(after) {
				t.Fatalf("apply calls = %d, конфиг изменен: %v", got.calls, string(before) != string(after))
			}
		})
	}
}

func TestGetSources_States(t *testing.T) {
	s, _, home := newSourcesSettings(t)
	if err := s.SetSources([]desktopconfig.Source{
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
		src("folder-2", desktopconfig.KindFolder, filepath.Join(home, "work"), false),
		src("catalog-1", desktopconfig.KindCatalog, filepath.Join(home, "catalog.json"), true),
		src("server-1", desktopconfig.KindServer, "https://kb.example", true),
	}, []string{"finances"}); err != nil {
		t.Fatal(err)
	}
	// Папку удалили после сохранения: экран называет это, а не показывает «ок».
	if err := os.Remove(filepath.Join(home, "work")); err != nil {
		t.Fatal(err)
	}
	s.cfg.Sources[1].Enabled = true
	v := s.GetSources()
	type row struct {
		id, state string
		count     int
	}
	var rows []row
	for _, r := range v.Sources {
		rows = append(rows, row{r.ID, r.State, r.Count})
	}
	want := []row{{"folder-1", stateOK, 7}, {"folder-2", stateMissing, 0}, {"catalog-1", stateOK, 42}, {"server-1", statePending, 0}}
	if !slices.Equal(rows, want) || !slices.Equal(v.Exclude, []string{"finances"}) {
		t.Fatalf("rows = %+v exclude = %q", rows, v.Exclude)
	}
	if v.Sources[1].Note == "" || v.Sources[3].Note == "" {
		t.Fatalf("у пропавшей папки и сервера нет пояснения: %+v", v.Sources)
	}
	s.cfg.Sources[0].Enabled = false
	if r := s.GetSources().Sources[0]; r.State != stateOff {
		t.Fatalf("выключенная = %+v", r)
	}
}

// Каталог переключается без перезапуска окна: поиск по базе читает тот путь,
// который сейчас в настройках.
func TestCatalogSwitch_FollowsPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(p, []byte(`{"entries":[{"id":1,"title":"Как читать договор","url":"https://h/a","category":"golang","status":"keep","lifecycle":"active"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var sw catalogSwitch
	c, err := sw.Load()
	if err != nil || c.Len() != 0 {
		t.Fatalf("пустой путь: len=%v err=%v", c, err)
	}
	sw.set(p)
	c, err = sw.Load()
	if err != nil || c.Len() != 1 {
		t.Fatalf("после set: err=%v", err)
	}
	if n, err := sw.count(); err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
}

// Все включенные папки — сохраненные, но сейчас недоступные: сохранение
// отказывает, а не затирает индекс пустым (старый Set так и делал).
func TestSetSources_AllEnabledMissingIsRejected(t *testing.T) {
	s, got, _ := newSourcesSettings(t)
	gone := s.cfg.FolderRoots()[0] // ~/claude-cowork во временном доме отсутствует
	err := s.SetSources([]desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, gone, true)}, nil)
	if !errors.Is(err, errNoFolders) || got.calls != 0 {
		t.Fatalf("err = %v, apply calls = %d", err, got.calls)
	}
}

// Сохраненный каталог, которого сейчас нет (сетевой диск), не мешает
// сохранению — как и папка. Новый отсутствующий каталог по-прежнему отказ.
func TestSetSources_KnownMissingCatalogIsKept(t *testing.T) {
	s, got, _ := newSourcesSettings(t)
	cat := s.cfg.CatalogPath() // умолчание: каталога во временном доме нет
	err := s.SetSources([]desktopconfig.Source{
		src("", desktopconfig.KindFolder, "~/notes", true),
		src("catalog-1", desktopconfig.KindCatalog, cat, true),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.catalog != cat {
		t.Fatalf("apply catalog = %q, want %q", got.catalog, cat)
	}
}

// Пока индекс не собран, счет неизвестен: экран говорит «индексируется», а
// не «0 файлов».
func TestGetSources_BeforeFirstBuild(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	if err := s.SetSources([]desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, "~/notes", true)}, nil); err != nil {
		t.Fatal(err)
	}
	s.countFiles = func([]string) map[string]int { return nil }
	if r := s.GetSources().Sources[0]; r.State != stateIndexing {
		t.Fatalf("row = %+v", r)
	}
}

// Сохраненная папка вернулась (VPN подключили): reconcile открывает ее
// заново, не дожидаясь сохранения. Пока не открыл — экран не зовет ее «ок».
func TestReconcile_ReopensReturnedFolder(t *testing.T) {
	s, got, home := newSourcesSettings(t)
	team := filepath.Join(home, "team")
	if err := os.MkdirAll(team, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources([]desktopconfig.Source{
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
		src("folder-2", desktopconfig.KindFolder, team, true),
	}, nil); err != nil {
		t.Fatal(err)
	}
	// Диск отвалился, сохранили перестановку — папка вне индекса.
	if err := os.Rename(team, team+".off"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources([]desktopconfig.Source{
		src("folder-2", desktopconfig.KindFolder, team, true),
		src("folder-1", desktopconfig.KindFolder, "~/notes", true),
	}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.roots, []string{filepath.Join(home, "notes")}) {
		t.Fatalf("roots = %q", got.roots)
	}
	if s.reconcile() {
		t.Fatal("reconcile без изменений на диске что-то применил")
	}
	// Диск вернулся.
	if err := os.Rename(team+".off", team); err != nil {
		t.Fatal(err)
	}
	if r := s.GetSources().Sources[0]; r.State != stateIndexing {
		t.Fatalf("вернувшаяся, но не открытая папка = %+v", r)
	}
	calls := got.calls
	if !s.reconcile() || got.calls != calls+1 || !slices.Equal(got.roots, []string{team, filepath.Join(home, "notes")}) {
		t.Fatalf("reconcile: calls %d→%d roots %q", calls, got.calls, got.roots)
	}
	if r := s.GetSources().Sources[0]; r.State != stateOK {
		t.Fatalf("после reconcile = %+v", r)
	}
	if s.reconcile() {
		t.Fatal("повторный reconcile применил снова")
	}
}

// reconcile никогда не открывает пустой набор: пропали все папки — индекс
// остается прежним, а не затирается.
func TestReconcile_NeverAppliesEmpty(t *testing.T) {
	s, got, home := newSourcesSettings(t)
	if err := s.SetSources([]desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, "~/notes", true)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(home, "notes"), filepath.Join(home, "notes.off")); err != nil {
		t.Fatal(err)
	}
	calls := got.calls
	if s.reconcile() || got.calls != calls {
		t.Fatal("reconcile применил пустой набор")
	}
}
