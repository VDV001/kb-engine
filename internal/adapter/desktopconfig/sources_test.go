package desktopconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

func writeRaw(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Конфиг до источников (roots + catalog) становится списком источников при
// чтении: папки по порядку, потом каталог. Человек, обновивший окно, не теряет
// ни одной папки и не настраивает ничего заново.
func TestRead_MigratesRootsAndCatalogToSources(t *testing.T) {
	path := writeRaw(t, `{"roots":["/a","/b"],"exclude":["finances"],"catalog":"/kb/catalog.json","server":"https://kb.example"}`)
	cfg, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []desktopconfig.Source{
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: "/a", Enabled: true},
		{ID: "folder-2", Kind: desktopconfig.KindFolder, Path: "/b", Enabled: true},
		{ID: "catalog-1", Kind: desktopconfig.KindCatalog, Path: "/kb/catalog.json", Enabled: true},
		// Сервер раньше не читался никем: переезжает выключенным, чтобы окно не
		// начало ходить по сети само, просто от обновления.
		{ID: "server-1", Kind: desktopconfig.KindServer, URL: "https://kb.example", Enabled: false},
	}
	if !slices.Equal(cfg.Sources, want) {
		t.Fatalf("sources =\n%+v\nwant\n%+v", cfg.Sources, want)
	}
}

// Порядок в списке — приоритет: папки окна отдаются в том порядке, в каком
// стоят, выключенные и чужие виды пропускаются.
func TestConfig_FolderRootsAndCatalogFollowOrder(t *testing.T) {
	cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "catalog-1", Kind: desktopconfig.KindCatalog, Path: "/off.json", Enabled: false},
		{ID: "folder-2", Kind: desktopconfig.KindFolder, Path: "/second", Enabled: true},
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: "/off", Enabled: false},
		{ID: "catalog-2", Kind: desktopconfig.KindCatalog, Path: "/kb.json", Enabled: true},
		{ID: "folder-3", Kind: desktopconfig.KindFolder, Path: "/third", Enabled: true},
	}}
	if got := cfg.FolderRoots(); !slices.Equal(got, []string{"/second", "/third"}) {
		t.Errorf("FolderRoots = %q", got)
	}
	if got := cfg.CatalogPath(); got != "/kb.json" {
		t.Errorf("CatalogPath = %q, want первый включенный каталог", got)
	}
}

func TestConfig_Validate(t *testing.T) {
	folder := func(id, path string) desktopconfig.Source {
		return desktopconfig.Source{ID: id, Kind: desktopconfig.KindFolder, Path: path, Enabled: true}
	}
	server := func(url string) desktopconfig.Source {
		return desktopconfig.Source{ID: "server-1", Kind: desktopconfig.KindServer, URL: url, Enabled: true}
	}
	tests := []struct {
		name    string
		sources []desktopconfig.Source
		want    error
	}{
		{"пустой список", nil, nil},
		{"папка", []desktopconfig.Source{folder("folder-1", "/a")}, nil},
		{"https-сервер", []desktopconfig.Source{server("https://kb.example")}, nil},
		{"http только на этом Mac", []desktopconfig.Source{server("http://127.0.0.1:8097")}, nil},
		{"http localhost", []desktopconfig.Source{server("http://localhost:8097")}, nil},
		{"неизвестный вид", []desktopconfig.Source{{ID: "x-1", Kind: "ftp", Path: "/a", Enabled: true}}, desktopconfig.ErrSourceKind},
		{"нет id", []desktopconfig.Source{folder("", "/a")}, desktopconfig.ErrSourceID},
		{"повтор id", []desktopconfig.Source{folder("folder-1", "/a"), folder("folder-1", "/b")}, desktopconfig.ErrSourceID},
		{"папка без пути", []desktopconfig.Source{folder("folder-1", "")}, desktopconfig.ErrSourceTarget},
		{"каталог без пути", []desktopconfig.Source{{ID: "catalog-1", Kind: desktopconfig.KindCatalog}}, desktopconfig.ErrSourceTarget},
		{"сервер без адреса", []desktopconfig.Source{server("")}, desktopconfig.ErrSourceTarget},
		// Токен по открытому http в сеть — нельзя: его прочтет любой на пути.
		{"http в сеть", []desktopconfig.Source{server("http://kb.example")}, desktopconfig.ErrSourceTarget},
		{"не адрес", []desktopconfig.Source{server("kb.example")}, desktopconfig.ErrSourceTarget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := desktopconfig.Config{Sources: tt.sources}.Validate()
			if tt.want == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Испорченный список источников — отказ при чтении, как и испорченный JSON:
// окно не должно молча работать не с теми источниками.
func TestRead_InvalidSourcesIsAnError(t *testing.T) {
	path := writeRaw(t, `{"sources":[{"id":"folder-1","kind":"folder","path":"/a","enabled":true},{"id":"folder-1","kind":"folder","path":"/b","enabled":true}]}`)
	if _, err := desktopconfig.Read(path); !errors.Is(err, desktopconfig.ErrSourceID) {
		t.Fatalf("err = %v, want ErrSourceID", err)
	}
}

// Панель папок (до экрана источников) меняет только включенные папки: на
// место первой из них встают новые, остальные источники и выключенные папки
// сохраняют место и id. Папка, что уже была, сохраняет свой id и имя.
func TestConfig_SetFolderRoots(t *testing.T) {
	cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "server-1", Kind: desktopconfig.KindServer, URL: "https://kb.example", Enabled: true},
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Name: "работа", Path: "/a", Enabled: true},
		{ID: "folder-2", Kind: desktopconfig.KindFolder, Path: "/off", Enabled: false},
		{ID: "folder-3", Kind: desktopconfig.KindFolder, Path: "/b", Enabled: true},
		{ID: "catalog-1", Kind: desktopconfig.KindCatalog, Path: "/kb.json", Enabled: true},
	}}
	cfg.SetFolderRoots([]string{"/new", "/a"})
	want := []desktopconfig.Source{
		{ID: "server-1", Kind: desktopconfig.KindServer, URL: "https://kb.example", Enabled: true},
		{ID: "folder-4", Kind: desktopconfig.KindFolder, Path: "/new", Enabled: true},
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Name: "работа", Path: "/a", Enabled: true},
		{ID: "folder-2", Kind: desktopconfig.KindFolder, Path: "/off", Enabled: false},
		{ID: "catalog-1", Kind: desktopconfig.KindCatalog, Path: "/kb.json", Enabled: true},
	}
	if !slices.Equal(cfg.Sources, want) {
		t.Fatalf("sources =\n%+v\nwant\n%+v", cfg.Sources, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Без включенных папок новые встают в конец списка.
func TestConfig_SetFolderRoots_NoFoldersAppends(t *testing.T) {
	cfg := desktopconfig.Config{Sources: []desktopconfig.Source{
		{ID: "catalog-1", Kind: desktopconfig.KindCatalog, Path: "/kb.json", Enabled: true},
	}}
	cfg.SetFolderRoots([]string{"/a"})
	if got := cfg.FolderRoots(); !slices.Equal(got, []string{"/a"}) || cfg.Sources[0].ID != "catalog-1" {
		t.Fatalf("sources = %+v", cfg.Sources)
	}
}

// Запись оставляет roots и catalog производными от источников: их читает
// kbengine-mcp прежней сборки, который еще работает, пока его не пересобрали.
// Сами источники переживают круг записи и чтения без потерь.
func TestWrite_KeepsLegacyFieldsDerived(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := desktopconfig.Config{Exclude: []string{"finances"}, Sources: []desktopconfig.Source{
		{ID: "folder-1", Kind: desktopconfig.KindFolder, Path: "/a", Enabled: true},
		{ID: "folder-2", Kind: desktopconfig.KindFolder, Path: "/off", Enabled: false},
		{ID: "catalog-1", Kind: desktopconfig.KindCatalog, Path: "/kb.json", Enabled: true},
	}}
	if err := desktopconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Roots, []string{"/a"}) || got.Catalog != "/kb.json" {
		t.Errorf("legacy roots = %q catalog = %q", got.Roots, got.Catalog)
	}
	if !slices.Equal(got.Sources, cfg.Sources) {
		t.Errorf("sources = %+v", got.Sources)
	}
}

// Первый запуск сразу пишет источники: папка claude-cowork и каталог базы.
func TestDefault_HasSources(t *testing.T) {
	home := "/Users/x"
	cfg := desktopconfig.Default(home)
	if got := cfg.FolderRoots(); !slices.Equal(got, []string{filepath.Join(home, "claude-cowork")}) {
		t.Errorf("FolderRoots = %q", got)
	}
	if got := cfg.CatalogPath(); got != filepath.Join(home, "claude-cowork", "knowledge-base", "_data", "catalog.json") {
		t.Errorf("CatalogPath = %q", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Писатель прежней формы (roots без sources) не теряет папки: Write сначала
// переводит такой конфиг в источники, как Read. Ловили тесты kbengine-mcp.
func TestWrite_LegacyShapeMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := desktopconfig.Write(path, desktopconfig.Config{Roots: []string{"/a"}, Catalog: "/kb.json"}); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.FolderRoots(), []string{"/a"}) || got.CatalogPath() != "/kb.json" || !slices.Equal(got.Roots, []string{"/a"}) {
		t.Fatalf("cfg = %+v", got)
	}
}
