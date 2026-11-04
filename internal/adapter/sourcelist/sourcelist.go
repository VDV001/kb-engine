// Package sourcelist is what the window (desktop) and the MCP server share
// about the window's source list: какие источники включены и в каком порядке,
// как прочесть токен kb-сервера в срок, как строго разобрать запись чужого
// сервера и как искать в файле каталога. Раньше у окна и MCP были свои копии,
// и агент мог увидеть не то, что видит человек.
package sourcelist

import (
	"context"
	"encoding/json"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// Enabled lists the enabled sources of a known kind, in list order. Источник
// неизвестного вида (из более новой сборки) не идет: искать в нем нечем.
func Enabled(cfg desktopconfig.Config) []desktopconfig.Source {
	var out []desktopconfig.Source
	for _, s := range cfg.WithSources().Sources {
		switch s.Kind {
		case desktopconfig.KindCatalog, desktopconfig.KindServer, desktopconfig.KindFolder:
			if s.Enabled {
				out = append(out, s)
			}
		}
	}
	return out
}

// TokenWithin reads a token with get, giving up when ctx ends: Связка, ждущая
// разблокировки, не держит ответ. Брошенное чтение доживает само (security
// ограничен 10 с).
func TokenWithin(ctx context.Context, get func(string) (string, error), id string) (string, error) {
	type res struct {
		tok string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		t, err := get(id)
		ch <- res{t, err}
	}()
	select {
	case r := <-ch:
		return r.tok, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// SearchCatalog searches one catalog file the way /api/search does.
func SearchCatalog(path, q string, m search.Matcher) ([]domain.Entry, error) {
	cat, err := catalogjson.FileLoader{Path: path}.Load()
	if err != nil {
		return nil, err
	}
	return search.FilterWith(cat.Entries(), q, m), nil
}

// Entry is what is shown of a kb server's entry. Запись чужого сервера
// разбирается строго: поле не того типа — запись отброшена (номер-строка с
// разметкой не доедет до страницы или агента), наружу уходят только эти поля.
type Entry struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	URL         string   `json:"url,omitempty"`
	Category    string   `json:"category,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Lifecycle   string   `json:"lifecycle,omitempty"`
	Verdict     string   `json:"verdict,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Author      string   `json:"author,omitempty"`
	DateAdded   string   `json:"date_added,omitempty"`
	Description string   `json:"description,omitempty"`
	Match       []Match  `json:"match,omitempty"`
}

// Match is one «где и как совпало» of a server's entry; поле и способ — из
// тех, что называет search.Explain, иначе признак отброшен.
type Match struct {
	Word  string `json:"word"`
	Field string `json:"field"`
	Hit   string `json:"hit"`
	How   string `json:"how"`
}

// Decoded is a server's entry with its merge key.
type Decoded struct {
	Key   string
	Entry Entry
}

// Пределы признака находки чужого сервера: из слов признака окно строит
// подсветку на каждую строку выдачи, и сломанный сервер не должен подвесить ее
// тысячей признаков или словом-простыней. Наш сервер дает по признаку на слово
// запроса — единицы.
const (
	MaxMatches  = 16
	MaxMatchLen = 200
)

// DecodeEntries keeps the server entries that decode as Entry; адрес, не
// прошедший правило домена (http(s) с хостом), убирается — javascript: не
// откроется кнопкой «Открыть источник».
func DecodeEntries(raw []json.RawMessage) []Decoded {
	out := make([]Decoded, 0, len(raw))
	for _, r := range raw {
		// Признак — необязательное поле и разбирается отдельно: не того вида,
		// он отбрасывается сам, а запись с ключом склейки остается.
		var e Entry
		var rawMatch struct {
			Match json.RawMessage `json:"match"`
		}
		if json.Unmarshal(r, &rawMatch) != nil {
			continue
		}
		if len(rawMatch.Match) > 0 {
			r = withoutMatch(r)
		}
		if json.Unmarshal(r, &e) != nil || e.Title == "" {
			continue
		}
		var ms []Match
		_ = json.Unmarshal(rawMatch.Match, &ms) // не тот вид — признака нет
		// Ключ — по исходному адресу, как ключи остальных записей, что сервер
		// считает сам (sourcesearch.Split): склейка не зависит от того, попала
		// запись в присланные или нет. Показ — по проверенному адресу.
		key := sourcesearch.EntryKey(e.ID, e.Title, e.URL)
		e.Match = knownMatches(ms)
		if u, err := domain.NewExternalURL(e.URL); err != nil {
			e.URL = ""
		} else {
			e.URL = u.String() // обрезанный: и для кнопки, и для ключа склейки
		}
		out = append(out, Decoded{Key: key, Entry: e})
	}
	return out
}

// withoutMatch returns the entry with its match field removed: запись
// разбирается строго, признак — отдельно.
func withoutMatch(r json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(r, &m) != nil {
		return r
	}
	delete(m, "match")
	b, err := json.Marshal(m)
	if err != nil {
		return r
	}
	return b
}

// knownMatches keeps the matches with a field and way search can name: список
// живет в пакете search, а не здесь третьей копией.
func knownMatches(in []Match) []Match {
	var out []Match
	for _, m := range in {
		if len(out) == MaxMatches {
			break
		}
		if search.ValidField(m.Field) && search.ValidHow(m.How) && len(m.Word) <= MaxMatchLen && len(m.Hit) <= MaxMatchLen {
			out = append(out, m)
		}
	}
	return out
}
