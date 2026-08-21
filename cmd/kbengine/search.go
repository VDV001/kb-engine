package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/embedhttp"
	"github.com/daniil/kb-engine/internal/adapter/searchindex"
	"github.com/daniil/kb-engine/internal/adapter/searchsyn"
	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/query"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// defaultThreshold — порог близости по умолчанию.
//
// Число получено ЗАМЕРОМ на живом каталоге (1487 записей, модель bge-m3), а не
// выбрано круглым. Восемь запросов, четыре про то, что в базе есть, и четыре
// про то, чего в ней нет:
//
//	в базе:  0.611 · 0.598 · 0.563 · 0.548
//	вне неё: 0.419 · 0.373 · 0.368 · 0.367
//
// Между группами разрыв, и 0.50 лежит в нём с запасом в обе стороны. Прежнее
// умолчание 0.55 срезало бы верное попадание («почему падает контейнер в
// кубернетесе» — 0.548).
//
// ⚠️ Порог принадлежит МОДЕЛИ, а не движку: у другой модели своя шкала, и это
// же число там будет означать другое. Индекс хранит имя модели рядом с
// векторами именно поэтому.
const defaultThreshold = 0.50

func init() { commands["search"] = withoutStdin(runSearch) }

// runSearch ищет записи каталога и говорит, какими слоями искал.
//
// Отдельная команда, а не только экран терминала: слой, который можно позвать
// лишь интерактивно, нечем проверить в прогоне и нечем показать в отчёте — а
// «написано и никто не зовёт» этот движок ловил у себя трижды за август.
func runSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "path to catalog.json")
	q := fs.String("q", "", "поисковый запрос")
	indexPath := fs.String("index", "", "путь к векторному индексу (по умолчанию рядом с каталогом)")
	embedURL := fs.String("embed-url", "", "адрес службы эмбеддингов, например http://127.0.0.1:11434/api/embeddings")
	embedModel := fs.String("embed-model", "nomic-embed-text", "имя модели для службы эмбеддингов")
	threshold := fs.Float64("threshold", defaultThreshold, "порог близости для смыслового слоя")
	limit := fs.Int("limit", 10, "сколько записей показать")
	build := fs.Bool("build-index", false, "снять векторы всех записей и записать индекс")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogPath == "" {
		fmt.Fprintln(stderr, "search: --catalog is required")
		return 2
	}
	if *q == "" && !*build {
		fmt.Fprintln(stderr, "search: нужен --q или --build-index")
		return 2
	}

	entries, err := query.NewService(catalogjson.FileLoader{Path: *catalogPath}).Entries()
	if err != nil {
		fmt.Fprintf(stderr, "search: %v\n", err)
		return 1
	}
	ixPath := *indexPath
	if ixPath == "" {
		ixPath = searchindex.PathNextTo(*catalogPath)
	}

	if *build {
		return buildIndex(entries, ixPath, *embedURL, *embedModel, stdout, stderr)
	}

	// Текстовые слои работают всегда — они и есть основной путь.
	syn, synErr := searchsyn.Load(searchsyn.PathNextTo(*catalogPath))
	matcher := search.New(syn)
	text := tui.FilterWith(entries, *q, matcher)

	fmt.Fprintf(stdout, "запрос: %q\n\n", *q)
	fmt.Fprintf(stdout, "текстовый слой — %d\n", len(text))
	for _, e := range text[:min(*limit, len(text))] {
		fmt.Fprintf(stdout, "  #%-5d %s\n", e.ID(), e.Title())
	}

	// Смысловой слой не обязателен, и его отсутствие называется вслух: пустота
	// вместо ответа читается как «в базе такого нет».
	fmt.Fprintln(stdout)
	printSemantic(stdout, entries, ixPath, *embedURL, *embedModel, *q, *limit, *threshold)

	if synErr != nil {
		fmt.Fprintf(stdout, "\n⚠️ %v — термины не переводились\n", synErr)
	}
	return 0
}

// printSemantic печатает смысловой слой или причину, по которой его не было.
func printSemantic(stdout io.Writer, entries []domain.Entry,
	ixPath, embedURL, embedModel, q string, limit int, threshold float64,
) {
	sem, why := semanticLayer(ixPath, embedURL, embedModel)
	if why != "" {
		fmt.Fprintf(stdout, "смысловой слой не работал: %s\n", why)
		return
	}
	hits, err := sem.Search(q, limit, threshold)
	switch {
	case errors.Is(err, search.ErrNoSemanticLayer):
		fmt.Fprintln(stdout, "смысловой слой не работал: нет индекса или эмбеддера")
	case err != nil:
		fmt.Fprintf(stdout, "смысловой слой отказал: %v\n", err)
	case len(hits) == 0:
		fmt.Fprintf(stdout, "смысловой слой — ничего ближе порога %.2f\n", threshold)
	default:
		fmt.Fprintf(stdout, "смысловой слой — %d (порог %.2f)\n", len(hits), threshold)
		byID := map[int]domain.Entry{}
		for _, e := range entries {
			byID[e.ID()] = e
		}
		for _, h := range hits {
			title := "(записи с таким id в каталоге нет)"
			if e, ok := byID[h.ID]; ok {
				title = e.Title()
			}
			fmt.Fprintf(stdout, "  %.3f  #%-5d %s\n", h.Score, h.ID, title)
		}
	}
}

// semanticLayer собирает смысловой слой и возвращает причину, если не смог.
func semanticLayer(indexPath, url, model string) (search.Semantic, string) {
	if url == "" {
		return search.Semantic{}, "не задан --embed-url (служба эмбеддингов)"
	}
	ix, err := searchindex.Load(indexPath)
	if err != nil {
		return search.Semantic{}, err.Error()
	}
	return search.NewSemantic(ix, embedhttp.New(url, model)), ""
}

// buildIndex снимает векторы всех записей.
func buildIndex(entries []domain.Entry, path, url, model string, stdout, stderr io.Writer) int {
	if url == "" {
		fmt.Fprintln(stderr, "search --build-index: нужен --embed-url — снимать векторы нечем")
		return 2
	}
	client := embedhttp.New(url, model)
	vectors := map[string][]float32{}
	dims := 0
	failed := 0
	for i, e := range entries {
		v, err := client.Embed(indexText(e))
		if err != nil {
			// Отказ на одной записи не повод бросать индекс: он копится и
			// считается. Молча пропустить значило бы отдать неполный индекс за
			// полный.
			failed++
			if failed <= 3 {
				fmt.Fprintf(stderr, "  запись #%d: %v\n", e.ID(), err)
			}
			continue
		}
		if dims == 0 {
			dims = len(v)
		}
		vectors[fmt.Sprintf("%d", e.ID())] = v
		if (i+1)%100 == 0 {
			fmt.Fprintf(stdout, "  снято %d из %d\n", i+1, len(entries))
		}
	}
	if len(vectors) == 0 {
		fmt.Fprintln(stderr, "search --build-index: не снято ни одного вектора — индекс не записан")
		return 1
	}
	body, err := json.MarshalIndent(map[string]any{
		"model": model, "dims": dims,
		"built":   time.Now().Format(time.RFC3339),
		"vectors": vectors,
	}, "", " ")
	if err != nil {
		fmt.Fprintf(stderr, "search --build-index: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "search --build-index: %v\n", err)
		return 1
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		fmt.Fprintf(stderr, "search --build-index: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "индекс записан: %s · записей %d из %d · измерений %d · модель %s\n",
		path, len(vectors), len(entries), dims, model)
	if failed > 0 {
		// Неполнота называется числом, а не умалчивается: индекс, где нет
		// трети записей, ищет по двум третям и выглядит при этом рабочим.
		fmt.Fprintf(stdout, "⚠️ не снято: %d — эти записи смысловой слой не найдёт\n", failed)
	}
	return 0
}

// indexText — то, по чему запись ищется смыслом.
func indexText(e domain.Entry) string {
	var b strings.Builder
	b.WriteString(e.Title())
	if d := e.Description(); d != "" {
		b.WriteString(". ")
		b.WriteString(d)
	}
	for _, t := range e.Tags() {
		b.WriteByte(' ')
		b.WriteString(t)
	}
	return b.String()
}

// indexGapLine — заглушка, поведение появится следующим коммитом.
func indexGapLine(_ search.Index, _ []int) string { return "" }
