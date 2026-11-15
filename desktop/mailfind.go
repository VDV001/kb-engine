package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// Поиск с телефона (#622; решение владельца 15.11 — чат и поиск сразу): тот же
// поиск, что у чата окна (база, kb-серверы, папки), и чтение найденного.
// «Доступы» в нем не участвуют — пароли на телефон не уходят никогда.

const (
	phoneHits    = 20  // столько строк выдачи уходит телефону
	phoneSnippet = 160 // знаков начала документа в строке выдачи
	phoneShown   = 500 // столько ref помнится для «открыть»
)

var errNotShown = errors.New("этого документа нет в выдаче телефона — найдите его снова")

// phoneFinder searches for the phone and opens only what it has shown: ref,
// придуманный телефоном или чужим кодом, — отказ, как у модели (ревью #467).
type phoneFinder struct {
	search func(ctx context.Context, q string, limit int) ([]chat.Doc, error)
	read   func(ctx context.Context, ref string) (chat.Doc, error)
	mu     sync.Mutex
	shown  map[string]bool
}

func newPhoneFinder(search func(context.Context, string, int) ([]chat.Doc, error), read func(context.Context, string) (chat.Doc, error)) *phoneFinder {
	return &phoneFinder{search: search, read: read, shown: map[string]bool{}}
}

func (f *phoneFinder) Search(ctx context.Context, q string) ([]mailrelay.Hit, error) {
	if f.search == nil {
		return nil, errors.New("поиск окна еще не готов — повторите через минуту")
	}
	docs, err := f.search(ctx, q, phoneHits)
	if err != nil {
		return nil, err
	}
	hits := make([]mailrelay.Hit, 0, len(docs))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.shown) > phoneShown {
		f.shown = map[string]bool{}
	}
	for _, d := range docs {
		f.shown[d.Ref] = true
		hits = append(hits, mailrelay.Hit{Ref: d.Ref, Kind: hitKind(d.Ref), Title: d.Title, Snippet: snippet(d.Text), Where: hitWhere(d)})
	}
	return hits, nil
}

func (f *phoneFinder) Open(ctx context.Context, ref string) (mailrelay.Hit, error) {
	f.mu.Lock()
	ok := f.shown[ref]
	f.mu.Unlock()
	if !ok || f.read == nil {
		return mailrelay.Hit{}, errNotShown
	}
	d, err := f.read(ctx, ref)
	if err != nil {
		return mailrelay.Hit{}, err
	}
	return mailrelay.Hit{Ref: ref, Kind: hitKind(ref), Title: d.Title, Where: hitWhere(d), Text: d.Text}, nil
}

// hitKind — подпись слева в строке выдачи: расширение файла или «запись».
func hitKind(ref string) string {
	if p, ok := strings.CutPrefix(ref, "file:"); ok {
		if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(p)), "."); ext != "" {
			return ext
		}
		return "файл"
	}
	return "запись"
}

// hitWhere — где лежит: папка файла, иначе имя источника.
func hitWhere(d chat.Doc) string {
	if p, ok := strings.CutPrefix(d.Ref, "file:"); ok {
		return filepath.Base(filepath.Dir(p))
	}
	return d.Source
}

// snippet — начало текста одной строкой.
func snippet(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	if r := []rune(s); len(r) > phoneSnippet {
		return string(r[:phoneSnippet]) + "…"
	}
	return s
}

// maxSources — сколько меток «взято» уходит телефону.
const maxSources = 8

// answerSources are the «взято» labels of the chat's last answer: подключения и
// внешнее (Uses), потом документы ответа по заголовкам, без повторов.
func answerSources(rec chatRecord) []mailrelay.Source {
	n := len(rec.Messages)
	if n == 0 {
		return nil
	}
	last := rec.Messages[n-1]
	titles := map[string]string{}
	for _, d := range rec.Docs {
		titles[d.Ref] = d.Title
	}
	var out []mailrelay.Source
	seen := map[string]bool{}
	add := func(kind, name string) {
		if name != "" && !seen[kind+"|"+name] && len(out) < maxSources {
			seen[kind+"|"+name] = true
			out = append(out, mailrelay.Source{Kind: kind, Name: name})
		}
	}
	for _, u := range last.Uses {
		add(string(u.Kind), u.Name)
	}
	for _, ref := range last.Docs {
		add("doc", titles[ref])
	}
	return out
}
