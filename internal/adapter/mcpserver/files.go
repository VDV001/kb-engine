package mcpserver

import (
	"errors"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// FileSearcher — порт индекса файлов: тот же набор, что у HTTP-адаптера, и
// объявлен здесь заново по той же причине, что Querier, — интерфейс
// принадлежит потребителю.
type FileSearcher interface {
	Search(query string, limit int) ([]filesearch.Hit, error)
	Read(path string) (string, error)
	Stats() filesearch.Stats
}

const (
	defaultFilesLimit = 20
	// maxFilesLimit ниже, чем у HTTP (200): ответ целиком ложится в контекст
	// агента, и полсотни сниппетов уже больше, чем он прочтет внимательно.
	maxFilesLimit = 50
)

// ReadChunkRunes — сколько рун read_file отдает за вызов. Клиент режет вывод
// MCP по токенам (у Claude Code порог 25 тыс.), а файл в индексе бывает до
// 2 МБ; 20 тыс. рун кириллицы укладываются в порог с запасом.
const ReadChunkRunes = 20000

// Отказы инструментов. Текст уходит агенту как есть, поэтому каждый говорит,
// что сделать дальше.
var (
	errEmptyQuery   = errors.New("нужен запрос: пустой поиск по файлам отдал бы все подряд")
	errIndexPending = errors.New("индекс файлов еще собирается, повторите через несколько секунд")
	errEmptyPath    = errors.New("нужен путь к файлу, например из выдачи search_files")
	errNotAllowed   = errors.New("путь не разрешен: читаются только файлы из папок поиска, без секретов и служебных папок")
	errNotFound     = errors.New("файл не найден")
	errBadOffset    = errors.New("смещение вне файла: берите next_offset из прошлого ответа")
)

type fileSearchArgs struct {
	Query string `json:"query" jsonschema:"слова запроса; ищутся по содержимому и имени файла"`
	Limit int    `json:"limit,omitempty" jsonschema:"сколько файлов вернуть, по умолчанию 20, не больше 50"`
}

type fileReadArgs struct {
	Path   string `json:"path" jsonschema:"абсолютный путь из выдачи search_files"`
	Offset int    `json:"offset,omitempty" jsonschema:"с какой руны читать; next_offset прошлого ответа"`
}

type fileHitOut struct {
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Size     int64   `json:"size"`
	Modified string  `json:"modified"`
	Score    float64 `json:"score"`
	Snippet  string  `json:"snippet"`
}

// addFileTools registers search_files and read_file over f.
func addFileTools(s *mcp.Server, j journal, f FileSearcher) {
	addTool(s, j, &mcp.Tool{
		Name: "search_files",
		Description: "Поиск по файлам владельца на этой машине: заметки, документы, " +
			"код в папках, выбранных в окне kb-desktop. Отдает путь, строку " +
			"совпадения и размер индекса; целиком файл читает read_file.",
	}, func(in fileSearchArgs) []string {
		if in.Query == "" {
			return nil
		}
		return []string{in.Query}
	}, func(in fileSearchArgs) (map[string]any, error) { return searchFiles(f, in) })

	addTool(s, j, &mcp.Tool{
		Name: "read_file",
		Description: "Текст одного файла из папок поиска, кусками по 20 тыс. символов: " +
			"если next_offset не ноль, вызовите снова с offset = next_offset. " +
			"Читаются только файлы, которые индексирует search_files.",
	}, func(in fileReadArgs) []string {
		return []string{in.Path}
	}, func(in fileReadArgs) (map[string]any, error) { return readFile(f, in) })
}

func searchFiles(f FileSearcher, in fileSearchArgs) (map[string]any, error) {
	if in.Query == "" {
		return nil, errEmptyQuery
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultFilesLimit
	}
	hits, err := f.Search(in.Query, min(limit, maxFilesLimit))
	if errors.Is(err, filesearch.ErrNotBuilt) {
		return nil, errIndexPending
	}
	if err != nil {
		return nil, err
	}
	out := make([]fileHitOut, 0, len(hits))
	for _, h := range hits {
		out = append(out, fileHitOut{
			Path: h.Doc.Path(), Name: h.Doc.Name(), Kind: h.Doc.Kind().String(),
			Size: h.Doc.Size(), Modified: h.Doc.ModTime().UTC().Format(time.RFC3339),
			Score: h.Score, Snippet: h.Snippet,
		})
	}
	// Размер индекса идет вместе с выдачей: «не нашлось» без него не
	// отличить от «не искали».
	st := f.Stats()
	return map[string]any{"indexed": st.Indexed, "skipped": st.Skipped, "found": len(out), "hits": out}, nil
}

// readFile returns one chunk of a file's text starting at in.Offset runes.
func readFile(f FileSearcher, in fileReadArgs) (map[string]any, error) {
	if in.Path == "" {
		return nil, errEmptyPath
	}
	text, err := f.Read(in.Path)
	switch {
	case errors.Is(err, filesearch.ErrNotAllowed):
		return nil, errNotAllowed
	case err != nil:
		// Причину не раскрываем: «нет файла» и «нет прав» по любому пути
		// были бы способом простучать диск — то же правило, что в HTTP API.
		return nil, errNotFound
	}
	runes := []rune(text)
	if in.Offset < 0 || in.Offset > len(runes) {
		return nil, errBadOffset
	}
	end := min(in.Offset+ReadChunkRunes, len(runes))
	out := map[string]any{"path": in.Path, "text": string(runes[in.Offset:end]), "total_runes": len(runes)}
	// Последний кусок продолжения не обещает: next_offset появляется,
	// только когда за ним есть текст.
	if end < len(runes) {
		out["next_offset"] = end
	}
	return out, nil
}
