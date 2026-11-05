package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

// ErrBadWrite marks a write the catalog's rules refused: недопустимое поле,
// необъявленная категория, неизвестный id. Ответ 400 с причиной — ее можно
// показать человеку как есть.
var ErrBadWrite = errors.New("bad write")

// ErrNoEntry — правка записи, которой в каталоге нет: ответ 404.
var ErrNoEntry = errors.New("no such entry")

// maxWriteBody bounds a write request: запись каталога — это несколько строк,
// а не загрузка файла.
const maxWriteBody = 64 << 10

// EntryAdd is someone else's material to add: то же, что kbengine add --url.
// Своих артефактов (--file) через API нет: файл заметки должен лежать в
// хранилище, а загрузки файлов у сервера пока нет.
type EntryAdd struct {
	Title, Category, URL, Verdict, Author, Description, Lifecycle, Source string
	Tags                                                                  []string
}

// EntryPatch is an edit of one entry. nil — поле не трогать: пустая строка и
// «не передано» различаются, иначе забытое поле стирало бы значение.
type EntryPatch struct {
	Lifecycle, Verdict, Category, Title, Description, Notes, Author, URL *string
	AddTags, RemoveTags                                                  []string
}

func (p EntryPatch) empty() bool {
	for _, f := range []*string{p.Lifecycle, p.Verdict, p.Category, p.Title, p.Description, p.Notes, p.Author, p.URL} {
		if f != nil {
			return false
		}
	}
	return len(p.AddTags) == 0 && len(p.RemoveTags) == 0
}

// AddedEntry is the entry a POST produced, or found already in the catalog.
type AddedEntry struct {
	ID                int
	Title             string
	Already           bool
	CategoryUnchecked bool // в каталоге нет meta.categories: категорию не с чем сверить
}

// CatalogWriter writes the catalog the server reads. Реализует его тот, кто
// знает правила записи (cmd: ingest и catalogjson, те же, что у kbengine add
// и set), обработчик только разбирает запрос и переводит ответ.
type CatalogWriter interface {
	AddEntry(ctx context.Context, a EntryAdd) (AddedEntry, error)
	EditEntry(ctx context.Context, id int, p EntryPatch) (int, error)
}

// WithCatalogWriter подключает запись. Без нее маршрутов записи нет: сервер
// над файлом на диске только читает, пишет туда CLI.
func WithCatalogWriter(w CatalogWriter) Option {
	return func(o *options) { o.writer = w }
}

type entryAddDTO struct {
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	URL         string   `json:"url"`
	Verdict     string   `json:"verdict"`
	Author      string   `json:"author"`
	Description string   `json:"description"`
	Lifecycle   string   `json:"lifecycle"`
	Source      string   `json:"source"`
	Tags        []string `json:"tags"`
}

type entryPatchDTO struct {
	Lifecycle   *string  `json:"lifecycle"`
	Verdict     *string  `json:"verdict"`
	Category    *string  `json:"category"`
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Notes       *string  `json:"notes"`
	Author      *string  `json:"author"`
	URL         *string  `json:"url"`
	AddTags     []string `json:"add_tags"`
	RemoveTags  []string `json:"remove_tags"`
}

type addedDTO struct {
	ID                int    `json:"id"`
	Title             string `json:"title"`
	Already           bool   `json:"already,omitempty"`
	CategoryUnchecked bool   `json:"category_unchecked,omitempty"`
}

func handleAddEntry(wr CatalogWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in entryAddDTO
		if err := decodeStrict(w, r, &in); err != nil {
			writeBodyError(w, err)
			return
		}
		got, err := wr.AddEntry(r.Context(), EntryAdd(in))
		if err != nil {
			writeWriteError(w, "catalog", err)
			return
		}
		out := addedDTO(got)
		if !got.Already {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		writeJSON(w, out)
	}
}

func handleEditEntry(wr CatalogWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			http.Error(w, "id must be a positive number", http.StatusBadRequest)
			return
		}
		var in entryPatchDTO
		if err := decodeStrict(w, r, &in); err != nil {
			writeBodyError(w, err)
			return
		}
		p := EntryPatch(in)
		if p.empty() {
			http.Error(w, "nothing to change", http.StatusBadRequest)
			return
		}
		n, err := wr.EditEntry(r.Context(), id, p)
		if err != nil {
			writeWriteError(w, "catalog", err)
			return
		}
		// Настоящее число: «готово» без правки — та же тишина, от которой
		// починен kbengine set (reportSet).
		writeJSON(w, map[string]int{"updated": n})
	}
}

// decodeStrict reads exactly one JSON object of known fields, not longer than
// maxWriteBody: опечатка в имени поля — отказ, а не молча потерянное значение.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWriteBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body: exactly one JSON object expected")
	}
	return nil
}

// writeBodyError answers a body that could not be read: слишком большое —
// 413, чтобы клиент понял, что дело в размере, а не в содержании.
func writeBodyError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		http.Error(w, fmt.Sprintf("body over %d bytes", maxWriteBody), http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// sameSiteJSON guards the write routes against requests from other sites.
//
// Браузер шлет «простой» запрос (text/plain, form) на чужой адрес без
// предварительной проверки, и без этой защиты любая открытая страница
// писала бы в каталог сервера на 127.0.0.1 без токена. JSON такой страницы
// без разрешения CORS не отправить, а сервер его не дает. Сверху — отказ
// запросу, который браузер сам пометил как межсайтовый, и запросу с чужим
// Origin. CLI и окно Origin не шлют и проходят.
func sameSiteJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			http.Error(w, "write requests take Content-Type: application/json", http.StatusUnsupportedMediaType)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site write refused", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, "cross-site write refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// writeWriteError maps a write failure of subject (catalog, journal) to a
// status. Причина 500 остается в журнале сервера: в ней ключ хранилища.
func writeWriteError(w http.ResponseWriter, subject string, err error) {
	switch {
	case errors.Is(err, ErrBadWrite):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNoEntry):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, blob.ErrConflict):
		http.Error(w, "the "+subject+" kept changing under the write; retry", http.StatusConflict)
	default:
		log.Printf("%s write: %v", subject, err)
		http.Error(w, "write failed", http.StatusInternalServerError)
	}
}
