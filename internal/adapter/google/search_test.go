package google_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/google"
)

// Поиск по папке — запрос Drive fullText в ее детях, без корзины; кавычка в
// запросе человека экранируется, а не ломает запрос Drive.
func TestSearch(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		if r.URL.Path != "/files" || r.URL.Query().Get("supportsAllDrives") != "true" || r.URL.Query().Get("includeItemsFromAllDrives") != "true" {
			t.Errorf("запрос: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"files":[{"id":"d1","name":"ТЗ","mimeType":"application/vnd.google-apps.document","webViewLink":"https://docs.google.com/document/d/d1/edit","modifiedTime":"2026-11-01T10:00:00Z"}]}`)
	}))
	defer srv.Close()
	files, err := google.Client{APIURL: srv.URL, HTTP: srv.Client()}.Search(context.Background(), "at", "F1", `срок 'поставки' \ x`)
	if err != nil || len(files) != 1 || files[0].Name != "ТЗ" || files[0].Link != "https://docs.google.com/document/d/d1/edit" || files[0].Modified.IsZero() {
		t.Fatalf("files = %+v, %v", files, err)
	}
	want := `'F1' in parents and trashed = false and mimeType != 'application/vnd.google-apps.folder' and mimeType != 'application/vnd.google-apps.shortcut' and fullText contains 'срок \'поставки\' \\ x'`
	if gotQ != want {
		t.Fatalf("q = %s\nwant %s", gotQ, want)
	}
}
