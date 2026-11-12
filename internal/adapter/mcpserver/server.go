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
// Наружу уходит ровно то, что каталог и так публикует: список разрешенного
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

// DefaultSearchLimit — сколько записей search_catalog отдает без явного limit.
// Замер 08.11: «агент» в каталоге на 1620 записей давал 652 карточки, а «claude
// code» 189 (68 тыс. токенов); ответ остается в контексте и перечитывается
// каждым ходом. Из 12 вызовов журнала агента 8 были такими широкими.
const DefaultSearchLimit = 20

// MaxSearchLimit — потолок limit: весь каталог агенту по MCP не нужен.
const MaxSearchLimit = 200

type searchArgs struct {
	Query string `json:"query" jsonschema:"слова запроса; пустой запрос отдает начало каталога (см. limit)"`
	Limit int    `json:"limit,omitempty" jsonschema:"сколько записей отдать, по умолчанию 20; нашлось больше - в ответе total и more=true"`
}

// limitOf is the answer size the caller asked for; пусто или не больше нуля —
// предел по умолчанию, а не «без предела»: нужный весь каталог просят числом.
func (a searchArgs) limitOf() int {
	if a.Limit <= 0 {
		return DefaultSearchLimit
	}
	return min(a.Limit, MaxSearchLimit)
}

// markCut says that the answer shows fewer entries than the sources found:
// total — сколько нашлось, more — что часть не показана. Без усечения полей
// нет: «more» у полного ответа сбил бы агента искать то, чего нет.
func markCut(answer map[string]any, total, shown int) {
	if shown < total {
		answer["total"] = total
		answer["more"] = true
	}
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
			"источника, кроме своего каталога). Ответ не длиннее limit записей (по умолчанию 20): " +
			"нашлось больше - есть total и more=true, сузьте запрос или задайте limit (до 200). " +
			"С несколькими источниками предел свой у каждого: limit и не больше 40 от удаленного; " +
			"total - сколько нашли источники. Номер записи другого источника — " +
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
			entries, sources, total := mergeRemote(found, remote, in.Query, m, in.limitOf())
			answer := map[string]any{"sources": sources, "view": viewURL(cfg.ViewBase, "archives", in.Query)}
			markCut(answer, total, len(entries))
			answer["found"], answer["entries"] = len(entries), entries
			return answer, nil
		}
		answer := map[string]any{
			// Адрес витрины с той же выборкой: человек проверяет ответ по
			// первичным данным, а не по моему пересказу. Пустой, когда витрина
			// не объявлена — выдуманная ссылка хуже отсутствующей.
			"view": viewURL(cfg.ViewBase, "archives", in.Query),
		}
		total := len(found)
		found = found[:min(total, in.limitOf())]
		markCut(answer, total, len(found))
		out := make([]entryOut, 0, len(found))
		for _, e := range found {
			p := project(e)
			p.Match = sourcelist.MatchOf(search.Explain(e, in.Query, m))
			out = append(out, p)
		}
		answer["found"], answer["entries"] = len(out), out
		return answer, nil
	})

	addTool(s, j, &mcp.Tool{
		Name:        "get_entry",
		Description: "Карточка одной записи каталога по ее номеру.",
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
	if cfg.Machines != nil {
		addMachineTool(s, j, cfg.Machines)
	}
	if cfg.Memory != nil {
		addMemoryTools(s, j, cfg.Memory)
	}
	if cfg.Versions != nil {
		addVersionTools(s, j, cfg.Versions)
	}
	return s
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	// Без HTML-экранирования: ответ читает агент, а не браузер, и каждый «<»
	// в виде \u003c раздувал код и разметку из read_file вдвое (ревью #375).
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	// Без отступов: на тех же данных они стоили 24% токенов (замер 08.11).
	if err := enc.Encode(v); err != nil {
		return nil, nil, fmt.Errorf("ответ не сериализуется: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimSuffix(b.String(), "\n")}}}, nil, nil
}
