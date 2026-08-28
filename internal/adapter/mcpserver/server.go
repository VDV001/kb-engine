package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// entryOut — проекция записи наружу.
//
// Наружу уходит ровно то, что каталог и так публикует: список разрешённого
// здесь тот же, что у маршрута /kb/ — запись каталога. Личные заметки и книга
// финансов лежат в том же дереве и в каталоге не значатся, поэтому их путей в
// ответе появиться неоткуда.
type entryOut struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	URL         string   `json:"url,omitempty"`
	Category    string   `json:"category"`
	Kind        string   `json:"kind"`
	Lifecycle   string   `json:"lifecycle"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	DateAdded   string   `json:"date_added,omitempty"`
	File        string   `json:"file,omitempty"`
	RelatedIDs  []int    `json:"related_ids,omitempty"`
}

func project(e domain.Entry) entryOut {
	out := entryOut{
		ID:          e.ID(),
		Title:       e.Title(),
		URL:         e.URL(),
		Category:    e.Category().String(),
		Kind:        e.Kind(),
		Lifecycle:   e.Lifecycle().String(),
		Tags:        e.Tags(),
		Description: e.Description(),
		Author:      e.Author(),
		File:        e.NotesFile(),
		RelatedIDs:  e.RelatedIDs(),
	}
	// Дата опускается пустой намеренно: «даты нет» и «дата нулевая» у агента
	// выглядели бы одинаково, а решения он принимает по свежести.
	if t := e.DateAdded(); t != nil {
		out.DateAdded = t.Format(time.DateOnly)
	}
	return out
}

type searchArgs struct {
	Query string `json:"query" jsonschema:"слова запроса; пустой запрос отдаёт весь каталог"`
}

type entryArgs struct {
	ID int `json:"id" jsonschema:"номер записи каталога"`
}

type emptyArgs struct{}

// New собирает MCP-сервер над каталогом.
//
// Сервер — тонкий адаптер: каждый инструмент переводит вызов в тот же usecase,
// который отвечает витрине и терминалу. Своей фильтрации здесь нет ни строки, и
// это не стиль, а замер: пока правило жило в двух местах, «кубернетес» давал
// десять записей в терминале и ноль в браузере.
func New(q Querier, m search.Matcher, version, viewBase string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "kb-engine", Version: version}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_catalog",
		Description: "Поиск по каталогу базы знаний. Четыре слоя: подстрока, " +
			"транслитерация, опечатки, словарь синонимов. Слова соединяются через И, " +
			"«#123» адресует запись по номеру. Поле view — адрес витрины с той " +
			"же выборкой: его показывают человеку, чтобы он сверил ответ с " +
			"первичными данными, а не с пересказом.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchArgs) (*mcp.CallToolResult, any, error) {
		found, err := SearchCatalog(q, m, in.Query)
		if err != nil {
			return nil, nil, err
		}
		out := make([]entryOut, 0, len(found))
		for _, e := range found {
			out = append(out, project(e))
		}
		return jsonResult(map[string]any{
			"found":   len(out),
			"entries": out,
			// Адрес витрины с той же выборкой: человек проверяет ответ по
			// первичным данным, а не по моему пересказу. Пустой, когда витрина
			// не объявлена — выдуманная ссылка хуже отсутствующей.
			"view": viewURL(viewBase, "archives", in.Query),
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_entry",
		Description: "Карточка одной записи каталога по её номеру.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in entryArgs) (*mcp.CallToolResult, any, error) {
		e, err := GetEntry(q, in.ID)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{
			"entry": project(e),
			"view":  viewURL(viewBase, "archives", fmt.Sprintf("#%d", in.ID)),
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "stats",
		Description: "Сводка каталога: сколько записей, как они разложены по " +
			"категориям, состояниям и вердиктам.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
		st, err := q.Stats()
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{"stats": st, "view": viewURL(viewBase, "overview", "")})
	})

	return s
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("ответ не сериализуется: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}
