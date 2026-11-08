package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/daniil/kb-engine/internal/adapter/sourcelist"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// RemoteSearcher asks the window's source list: каталоги и kb-серверы в его
// порядке, выключенные не идут. Нулевой в Config — списка нет, и
// search_catalog отвечает своим каталогом, как прежде. Список из одного своего
// каталога — тоже прежняя форма ответа.
type RemoteSearcher interface {
	SearchRemote(ctx context.Context, q string) []RemoteFound
}

// RemoteSource names a kb server in the answer.
type RemoteSource struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// RemoteFound is what one source of the list answered. Own — это свой
// каталог MCP (--catalog): его записи ищет сам сервер, тем же поиском, что
// get_entry и stats. Catalog — найденное в другом каталоге списка. Entries и
// Keys — ответ kb-сервера: записи, как он их закодировал, и ключи склейки
// остальных найденных. CatalogMatch — чем найдены записи Catalog: признак
// находки считается тем же поиском, иначе разошелся бы с ним. Err — беда
// словами.
type RemoteFound struct {
	Source       RemoteSource
	Own          bool
	Catalog      []domain.Entry
	CatalogMatch search.Matcher
	Entries      []json.RawMessage
	Keys         []string
	Err          string
}

// legacyAnswer reports whether the list is just the own catalog: тогда форма
// ответа прежняя, без source и sources.
func legacyAnswer(remote []RemoteFound) bool {
	return remote == nil || (len(remote) == 1 && remote[0].Own && remote[0].Err == "")
}

// RemoteLimit — сколько записей одного сервера идет агенту; остальные —
// ключами склейки и в «найдено». Сервер 0.30.0 limit не знает и отдает все
// найденное — предел держит ответ, а не сервер.
const RemoteLimit = 40

// sourcedEntry is an entry of the merged answer with the source it came from.
type sourcedEntry struct {
	entryOut
	Source string `json:"source"`
}

// sourceOut is one source in the answer: сколько нашел или почему не ответил.
type sourceOut struct {
	RemoteSource
	Found int    `json:"found"`
	Error string `json:"error,omitempty"`
}

// catalogSource — id своего каталога MCP в ответе.
const catalogSource = "catalog"

type candidate struct {
	key   string
	entry *entryOut // nil — запись известна серверу только ключом
	// why — признак находки записи каталога; считается только для показанных,
	// как в окне: срез идет после склейки.
	why func() []sourcelist.Match
}

// mergeRemote joins the sources of the list by the rule of the window
// (sourcesearch.Merge): порядок — списка окна, дубль — у источника выше,
// запись, известная только ключом, в ответ не идет. Свой каталог отвечает под
// id catalog без предела: get_entry и read_file — только по нему.
func mergeRemote(own []domain.Entry, remote []RemoteFound, q string, m search.Matcher, limit int) ([]sourcedEntry, []sourceOut, int) {
	var lists []sourcesearch.Found[candidate]
	sources := []sourceOut{}
	ownSeen := false
	for _, r := range remote {
		src, items := r.Source, []candidate(nil)
		switch {
		case r.Own && ownSeen:
			continue // свой каталог дважды в списке окна отвечает раз
		case r.Own:
			ownSeen = true
			src.ID = catalogSource
			items = projected(own, false, q, m)
		case r.Catalog != nil:
			items = projected(r.Catalog, true, q, r.CatalogMatch)
		default:
			items = typedRemote(r.Entries)
			for _, k := range r.Keys {
				items = append(items, candidate{key: k})
			}
		}
		lists = append(lists, sourcesearch.Found[candidate]{Source: src.ID, Items: items})
		sources = append(sources, sourceOut{RemoteSource: src, Found: len(items), Error: r.Err})
	}
	out := []sourcedEntry{}
	shown := map[string]int{}
	total := 0
	for _, h := range sourcesearch.Merge(lists, func(c candidate) string { return c.key }) {
		total++ // нашли источники: показанные, ключи склейки и усеченные пределом
		// Предел у каждого источника свой: общий отдавал места первому, своему
		// каталогу, и сервер окна не показывал ни одной записи (ревью #519).
		// match считается ниже, только для показанных.
		capFor := limit
		if h.Source != catalogSource {
			capFor = min(limit, RemoteLimit)
		}
		if h.Item.entry == nil || shown[h.Source] >= capFor {
			continue
		}
		shown[h.Source]++
		e := *h.Item.entry
		if h.Item.why != nil {
			e.Match = h.Item.why()
		}
		out = append(out, sourcedEntry{entryOut: e, Source: h.Source})
	}
	return out, sources, total
}

// projected is a catalog's entries as candidates. У записи чужого каталога
// (foreign) путь к тексту и связи не берутся, как у записи сервера: get_entry и
// read_file открыли бы по ним свое.
func projected(entries []domain.Entry, foreign bool, q string, m search.Matcher) []candidate {
	out := make([]candidate, len(entries))
	for i, e := range entries {
		why := func() []sourcelist.Match { return sourcelist.MatchOf(search.Explain(e, q, m)) }
		p := project(e)
		if foreign {
			p.File, p.RelatedIDs = "", nil
		}
		out[i] = candidate{key: sourcesearch.EntryKey(e.ID(), e.Title(), e.URL()), entry: &p, why: why}
	}
	return out
}

// typedRemote decodes a server's entries strictly (sourcelist, как окно) into
// entryOut. Путь к тексту и связи у записи сервера не берутся: get_entry и
// read_file открыли бы по ним свое. Номер остается — его источник назван в
// source.
func typedRemote(raw []json.RawMessage) []candidate {
	dec := sourcelist.DecodeEntries(raw)
	out := make([]candidate, len(dec))
	for i, d := range dec {
		e := d.Entry
		out[i] = candidate{key: d.Key, entry: &entryOut{
			ID: e.ID, Title: e.Title, URL: e.URL, Category: e.Category, Kind: e.Kind,
			Lifecycle: e.Lifecycle, Tags: e.Tags, Description: e.Description,
			Author: e.Author, DateAdded: e.DateAdded, Match: e.Match,
		}}
	}
	return out
}
