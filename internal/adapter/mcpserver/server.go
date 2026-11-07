package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/sourcelist"
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
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	// Пустое поле опускается: у записи сервера, что поля не прислал, «неизвестно»
	// не должно выглядеть пустой категорией.
	Category    string   `json:"category,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Lifecycle   string   `json:"lifecycle,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	DateAdded   string   `json:"date_added,omitempty"`
	File        string   `json:"file,omitempty"`
	RelatedIDs  []int    `json:"related_ids,omitempty"`
	// Match — признак находки (где и как совпало), как в окне: у записи
	// каталога его считает search.Explain тем же поиском, что нашел, у записи
	// kb-сервера — тот, что прислал сервер.
	Match []sourcelist.Match `json:"match,omitempty"`
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
func New(q Querier, m search.Matcher, cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "kb-engine", Version: cfg.Version}, nil)
	j := newJournal(cfg)

	addToolCtx(s, j, &mcp.Tool{
		Name: "search_catalog",
		Description: "Поиск по каталогу базы знаний. Четыре слоя: подстрока, " +
			"транслитерация, опечатки, словарь синонимов. Слова соединяются через И, " +
			"«#123» адресует запись по номеру. У записи match — где и как совпало каждое " +
			"слово (поле, найденное, слой). Поле view — адрес витрины с той " +
			"же выборкой: его показывают человеку, чтобы он сверил ответ с " +
			"первичными данными, а не с пересказом. Когда подключен список источников " +
			"окна (kb-серверы, вторые каталоги), выдача общая и идет в его порядке: у " +
			"записи поле source, у ответа список sources — сколько нашел каждый " +
			"источник или почему не ответил; выключенный в окне источник не " +
			"спрашивается, а свой каталог, которого в списке нет, не ищется. found тогда — сколько записей в ответе (не больше 40 от " +
			"источника, кроме своего каталога). Номер записи другого источника — " +
			"номер в ЕГО каталоге: get_entry и read_file отвечают только за source=catalog.",
	}, func(in searchArgs) []string {
		// Пустой запрос аргументом не записывается: пустая строка в журнале и
		// отсутствие запроса выглядели бы одинаково, а означают разное.
		if in.Query == "" {
			return nil
		}
		return []string{in.Query}
	}, func(ctx context.Context, in searchArgs) (map[string]any, error) {
		found, err := SearchCatalog(q, m, in.Query)
		if err != nil {
			return nil, err
		}
		var remote []RemoteFound
		// Пустой запрос (весь каталог) тоже идет по списку окна: выключенный
		// там свой каталог не отдается целиком.
		if cfg.Remote != nil {
			remote = cfg.Remote.SearchRemote(ctx, in.Query)
		}
		if !legacyAnswer(remote) {
			// Список источников окна: одна выдача по правилу окна, у записи —
			// источник, у источника — найдено или беда. Список из одного своего
			// каталога — форма ответа прежняя.
			entries, sources := mergeRemote(found, remote, in.Query, m)
			return map[string]any{
				"found":   len(entries),
				"entries": entries,
				"sources": sources,
				"view":    viewURL(cfg.ViewBase, "archives", in.Query),
			}, nil
		}
		out := make([]entryOut, 0, len(found))
		for _, e := range found {
			p := project(e)
			p.Match = sourcelist.MatchOf(search.Explain(e, in.Query, m))
			out = append(out, p)
		}
		return map[string]any{
			"found":   len(out),
			"entries": out,
			// Адрес витрины с той же выборкой: человек проверяет ответ по
			// первичным данным, а не по моему пересказу. Пустой, когда витрина
			// не объявлена — выдуманная ссылка хуже отсутствующей.
			"view": viewURL(cfg.ViewBase, "archives", in.Query),
		}, nil
	})

	addTool(s, j, &mcp.Tool{
		Name:        "get_entry",
		Description: "Карточка одной записи каталога по её номеру.",
	}, func(in entryArgs) []string {
		return []string{fmt.Sprintf("#%d", in.ID)}
	}, func(in entryArgs) (map[string]any, error) {
		e, err := GetEntry(q, in.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"entry": project(e),
			"view":  viewURL(cfg.ViewBase, "archives", fmt.Sprintf("#%d", in.ID)),
		}, nil
	})

	addTool(s, j, &mcp.Tool{
		Name: "stats",
		Description: "Сводка каталога: сколько записей, как они разложены по " +
			"категориям, состояниям и вердиктам.",
	}, func(emptyArgs) []string {
		// Спрашивать сводку не о чем: аргументов у инструмента нет вовсе.
		return nil
	}, func(emptyArgs) (map[string]any, error) {
		st, err := q.Stats()
		if err != nil {
			return nil, err
		}
		return map[string]any{"stats": st, "view": viewURL(cfg.ViewBase, "overview", "")}, nil
	})

	if cfg.Files != nil {
		addFileTools(s, j, cfg.Files)
	}
	if cfg.Access != nil {
		addAccessTool(s, j, cfg.Access)
	}
	return s
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	// Без HTML-экранирования: ответ читает агент, а не браузер, и каждый «<»
	// в виде \u003c раздувал код и разметку из read_file вдвое (ревью #375).
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, nil, fmt.Errorf("ответ не сериализуется: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimSuffix(b.String(), "\n")}}}, nil, nil
}
