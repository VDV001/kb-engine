package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// FileSearcher is the file index the server exposes: поиск по файлам на диске
// и чтение одного файла под теми же правилами, что и индекс.
type FileSearcher interface {
	Search(query string, limit int) ([]filesearch.Hit, error)
	Read(path string) (string, error)
	Stats() filesearch.Stats
}

// WithFiles подключает поиск по файлам. Без нее маршрутов /api/files/* нет:
// сервер над одним каталогом не делает вид, что умеет искать по диску.
func WithFiles(f FileSearcher) Option {
	return func(o *options) { o.files = f }
}

const (
	defaultFilesLimit = 20
	// maxFilesLimit — потолок выдачи за запрос: на каждую строку читается файл
	// ради сниппета, и «limit=100000» превратил бы запрос в чтение диска.
	maxFilesLimit = 200
)

type fileHitDTO struct {
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Size     int64   `json:"size"`
	Modified string  `json:"modified"`
	Score    float64 `json:"score"`
	Snippet  string  `json:"snippet"`
}

type fileSearchDTO struct {
	Indexed int          `json:"indexed"`
	Skipped int          `json:"skipped"`
	Hits    []fileHitDTO `json:"hits"`
}

func handleFilesSearch(f FileSearcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q is required", http.StatusBadRequest)
			return
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			limit = defaultFilesLimit
		}
		hits, err := f.Search(q, min(limit, maxFilesLimit))
		switch {
		case errors.Is(err, filesearch.ErrNotBuilt):
			http.Error(w, "file index is still building", http.StatusServiceUnavailable)
			return
		case err != nil:
			writeError(w, err)
			return
		}
		st := f.Stats()
		out := fileSearchDTO{Indexed: st.Indexed, Skipped: st.Skipped, Hits: make([]fileHitDTO, 0, len(hits))}
		for _, h := range hits {
			out.Hits = append(out.Hits, fileHitDTO{
				Path: h.Doc.Path(), Name: h.Doc.Name(), Kind: h.Doc.Kind().String(),
				Size: h.Doc.Size(), Modified: h.Doc.ModTime().UTC().Format(time.RFC3339),
				Score: h.Score, Snippet: h.Snippet,
			})
		}
		writeJSON(w, out)
	}
}

func handleFilesRead(f FileSearcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}
		text, err := f.Read(path)
		switch {
		case errors.Is(err, filesearch.ErrNotAllowed):
			http.Error(w, "path is not allowed", http.StatusForbidden)
			return
		case err != nil:
			// Причину не раскрываем: ответ «нет файла» и «нет прав» по любому
			// пути был бы способом простучать диск.
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		writeJSON(w, struct {
			Path string `json:"path"`
			Text string `json:"text"`
		}{path, text})
	}
}

// localHosts — имена, под которыми к API обращаются с этой машины: loopback и
// схема окна Wails (wails://wails).
var localHosts = map[string]bool{
	"127.0.0.1": true, "localhost": true, "::1": true, "wails": true, "wails.localhost": true,
}

// localHostOnly отказывает запросу с чужим Host.
//
// Файлы с диска — самое личное, что отдает движок, а без этой проверки их
// читает любая страница через DNS rebinding: ее домен перепривязывается на
// 127.0.0.1, и запрос выглядит same-origin (ревью #371). Чужой Host значит,
// что браузер считает собеседником не этот сервер.
func localHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !localHosts[strings.Trim(host, "[]")] {
			http.Error(w, "files are served to this machine only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
