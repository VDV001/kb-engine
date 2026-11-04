package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/daniil/kb-engine/internal/adapter/sourcelist"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

// RemoteSearcher asks the kb servers of the window's source list. Нулевой в
// Config — серверов нет, и search_catalog отвечает, как прежде.
type RemoteSearcher interface {
	SearchRemote(ctx context.Context, q string) []RemoteFound
}

// RemoteSource names a kb server in the answer.
type RemoteSource struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// RemoteFound is what one server answered: записи, как сервер их закодировал,
// и ключи склейки остальных найденных; Err — беда словами.
type RemoteFound struct {
	Source  RemoteSource
	Entries []json.RawMessage
	Keys    []string
	Err     string
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
}

// mergeRemote joins the catalog's own entries with the servers' by the rule of
// the window (sourcesearch.Merge): свой каталог первым, дубль — у источника
// выше, запись, известная только ключом, в ответ не идет.
func mergeRemote(own []domain.Entry, remote []RemoteFound) ([]sourcedEntry, []sourceOut) {
	lists := []sourcesearch.Found[candidate]{{Source: catalogSource}}
	for _, e := range own {
		out := project(e)
		lists[0].Items = append(lists[0].Items, candidate{key: sourcesearch.EntryKey(e.ID(), e.Title(), e.URL()), entry: &out})
	}
	sources := []sourceOut{{RemoteSource: RemoteSource{ID: catalogSource}, Found: len(own)}}
	for _, r := range remote {
		items := typedRemote(r.Entries)
		for _, k := range r.Keys {
			items = append(items, candidate{key: k})
		}
		lists = append(lists, sourcesearch.Found[candidate]{Source: r.Source.ID, Items: items})
		sources = append(sources, sourceOut{RemoteSource: r.Source, Found: len(items), Error: r.Err})
	}
	out := []sourcedEntry{}
	shown := map[string]int{}
	for _, h := range sourcesearch.Merge(lists, func(c candidate) string { return c.key }) {
		if h.Item.entry == nil || (h.Source != catalogSource && shown[h.Source] == RemoteLimit) {
			continue
		}
		shown[h.Source]++
		out = append(out, sourcedEntry{entryOut: *h.Item.entry, Source: h.Source})
	}
	return out, sources
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
			Author: e.Author, DateAdded: e.DateAdded,
		}}
	}
	return out
}
