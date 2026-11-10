package desktopconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Выбранный браузер — путь к исполняемому файлу, который окно потом запускает:
// из конфига берется только абсолютный путь. Испорченное значение гасит выбор
// («автоматически»), а не окно: конфиг читает и MCP-сервер.
func TestBrowserPathKeptOrDropped(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"пусто — автоматически", "", ""},
		{"абсолютный путь", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
		{"имя без пути", "brave", ""},
		{"относительный путь", "../evil/chrome", ""},
		{"только пробелы", "   ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			raw, _ := json.Marshal(map[string]any{"browser": map[string]string{"path": tc.in}})
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Browser.Path != tc.want {
				t.Fatalf("Browser.Path = %q, want %q", cfg.Browser.Path, tc.want)
			}
		})
	}
}

func TestBrowserPathSurvivesWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default(t.TempDir())
	cfg.Browser.Path = "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"
	if err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil || got.Browser.Path != cfg.Browser.Path {
		t.Fatalf("после записи Browser.Path = %q, %v", got.Browser.Path, err)
	}
}
