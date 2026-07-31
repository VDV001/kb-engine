package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// Картинки владельца отдаются из его каталога, а не из бандла: скриншот
// проекта — такое же личное содержимое, как team.json, и в AGPL-репозиторий он
// не едет. Отсюда отдельная ветка вместо frontend/public.
func TestMedia_servesOwnerFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"floq.png":            &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n")},
		"shots/dealsense.png": &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n")},
	}
	srv := httptest.NewServer(http.StripPrefix("/media/", mediaHandler(fsys)))
	defer srv.Close()

	tests := []struct {
		name, path string
		wantCode   int
	}{
		{"file at root", "/media/floq.png", http.StatusOK},
		{"file in subdir", "/media/shots/dealsense.png", http.StatusOK},
		{"missing file", "/media/nope.png", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantCode)
			}
		})
	}
}

// Immutable здесь был бы неверен: имя картинки хеша не несёт, владелец
// перезаписывает файл тем же именем, и вечный кэш означал бы, что заменённый
// скриншот никто не увидит. Ровно этот класс ошибки уже стоил разбирательства
// с index.html.
func TestMedia_revalidates(t *testing.T) {
	fsys := fstest.MapFS{"floq.png": &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n")}}
	srv := httptest.NewServer(http.StripPrefix("/media/", mediaHandler(fsys)))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/media/floq.png")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

// Каталог картинок соседствует с финансами и каталогом знаний, поэтому выход
// за его пределы проверяется на настоящей файловой системе, а не на MapFS:
// защищает здесь os.DirFS, и проверять надо именно его.
func TestMedia_noEscapeFromDir(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	if err := os.Mkdir(media, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("ledger"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := httptest.NewServer(http.StripPrefix("/media/", mediaHandler(os.DirFS(media))))
	defer srv.Close()

	// Клиент net/http нормализует путь до отправки, поэтому запрос собирается
	// вручную: проверяется сервер, а не клиент.
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.URL.Opaque = "/media/../secret.txt"

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("status = 200: каталог отдал файл выше себя")
	}
}
