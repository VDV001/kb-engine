package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/embedhttp"
	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

const memoryUsage = "usage: kbengine memory <init|check|add|topics|pending|approve|reject|index|find> [flags] [dir]  (dir по умолчанию: ~/Library/Application Support/kb-desktop/dvk-memory)"

// memoryVerbs — подкоманды `kbengine memory`; у каждой свой набор флагов.
var memoryVerbs = map[string]func(args []string, stdout, stderr io.Writer) int{
	"init":    memoryInit,
	"check":   memoryCheck,
	"add":     memoryAdd,
	"topics":  memoryTopics,
	"pending": memoryPending,
	"approve": memoryApprove,
	"reject":  memoryReject,
	"index":   memoryIndex,
	"find":    memoryFind,
}

// runMemory — `kbengine memory init|check|add` (ADR-0008): скелет двухуровневой
// памяти, проверка размера горячего слоя и запись в холодный.
func runMemory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, memoryUsage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, memoryUsage)
		return 0
	}
	verb, ok := memoryVerbs[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "memory: неизвестная подкоманда %q\n%s\n", args[0], memoryUsage)
		return 2
	}
	return verb(args[1:], stdout, stderr)
}

// memoryDirArg разбирает флаги подкоманды и возвращает папку памяти: аргумент
// или папка по умолчанию рядом с настройками окна.
func memoryDirArg(fs *flag.FlagSet, args []string, stderr io.Writer) (dir string, code int, stop bool) {
	if code, stop := parseFlags(fs, args); stop {
		return "", code, true
	}
	switch fs.NArg() {
	case 0:
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "memory: папка по умолчанию недоступна: %v\n", err)
			return "", 1, true
		}
		return memorydir.DefaultPath(home), 0, false
	case 1:
		return fs.Arg(0), 0, false
	}
	fmt.Fprintln(stderr, memoryUsage)
	return "", 2, true
}

func memoryInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if err := memory.New(memorydir.New(dir), memory.Limits{}).Init(); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "memory: скелет в %s (HOT.md, cold/INDEX.md, journal/)\n", dir)
	return 0
}

func memoryCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("hot-limit", domain.DefaultHotLimit, "потолок горячего слоя в знаках")
	idxLimit := fs.Int("index-limit", domain.DefaultIndexLimit, "потолок cold/INDEX.md в знаках")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	ps, err := memory.New(memorydir.New(dir), memory.Limits{Hot: *limit, Index: *idxLimit}).Check()
	if err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	for _, p := range ps {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.Kind, p.File, p.Detail)
	}
	if len(ps) > 0 {
		return 1
	}
	fmt.Fprintln(stdout, "memory: ok")
	return 0
}

func memoryAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "имя записи: латиница, цифры, дефисы")
	topic := fs.String("topic", "", "тема записи: попадает в перечень тем горячего слоя")
	origin := fs.String("origin", "human", "кто пишет: human — прямо в холодный слой, agent — в очередь одобрения")
	desc := fs.String("description", "", "одна строка до 150 знаков — она же крючок в индексе")
	body := fs.String("body", "", "текст записи")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	by, err := domain.ParseOrigin(*origin)
	if err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 2
	}
	svc := memory.New(memorydir.New(dir), memory.Limits{})
	if by == domain.OriginAgent {
		if err := svc.Propose(*name, *topic, *desc, *body); err != nil {
			return memoryFail(stderr, err)
		}
		fmt.Fprintf(stdout, "memory: %s ждет одобрения (kbengine memory pending, approve --to cold|hot, reject)\n", *name)
		return 0
	}
	refreshed, err := svc.Add(*name, *topic, *desc, *body)
	if err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintf(stdout, "memory: добавлено %s.md и строка в индексе\n", *name)
	if !refreshed {
		fmt.Fprintln(stdout, "memory: в HOT.md нет меток перечня тем, он не обновлен (kbengine memory check назовет это)")
	}
	return 0
}

func memoryTopics(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory topics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	changed, err := memory.New(memorydir.New(dir), memory.Limits{}).Topics()
	if err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	if changed {
		fmt.Fprintln(stdout, "memory: перечень тем в HOT.md обновлен")
	} else {
		fmt.Fprintln(stdout, "memory: перечень тем актуален")
	}
	return 0
}

// memoryFail prints the error; неверный ввод — код 2, остальное — 1.
func memoryFail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "memory: %v\n", err)
	if errors.Is(err, domain.ErrInvalidNote) {
		return 2
	}
	return 1
}

func memoryPending(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory pending", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	list, err := memory.New(memorydir.New(dir), memory.Limits{}).Pending()
	if err != nil {
		return memoryFail(stderr, err)
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "memory: очередь пуста")
		return 0
	}
	for _, p := range list {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.Note.Name, p.Note.Topic, p.Note.Description)
	}
	return 0
}

func memoryApprove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "имя записи из очереди")
	to := fs.String("to", "cold", "слой: cold — запись и строка индекса, hot — одна строка правила в HOT.md")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	layer, err := domain.ParseLayer(*to)
	if err != nil {
		return memoryFail(stderr, err)
	}
	if err := memory.New(memorydir.New(dir), memory.Limits{}).Approve(*name, layer); err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintf(stdout, "memory: %s одобрена в слой %s\n", *name, layer)
	return 0
}

func memoryReject(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory reject", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "имя записи из очереди")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if err := memory.New(memorydir.New(dir), memory.Limits{}).Reject(*name); err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintf(stdout, "memory: %s отклонена, файл убран в journal/\n", *name)
	return 0
}

// Служба эмбеддингов по умолчанию — локальная Ollama с bge-m3 (ADR-0008). Без нее
// `find` ищет по тексту; к службе обращаются только если индекс векторов снят.
const (
	defaultEmbedURL   = "http://127.0.0.1:11434/api/embeddings"
	defaultEmbedModel = "bge-m3"
)

// memoryEmbedService собирает сценарий со смысловым слоем из флагов службы.
func memoryEmbedService(dir, url, model string, lim memory.Limits) *memory.Service {
	return memory.New(memorydir.New(dir), lim).WithEmbedder(embedhttp.New(url, model), model)
}

func memoryIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("embed-url", defaultEmbedURL, "адрес службы эмбеддингов (протокол Ollama)")
	model := fs.String("embed-model", defaultEmbedModel, "модель эмбеддингов")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	rep, err := memoryEmbedService(dir, *url, *model, memory.Limits{}).BuildIndex()
	if err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintf(stdout, "memory: индекс векторов записан: снято %d, переиспользовано %d, модель %s\n", rep.Embedded, rep.Reused, *model)
	if rep.Failed > 0 {
		fmt.Fprintf(stdout, "⚠️ не снято: %d — эти записи смысловой слой не найдет\n", rep.Failed)
	}
	return 0
}

func memoryFind(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory find", flag.ContinueOnError)
	fs.SetOutput(stderr)
	q := fs.String("q", "", "запрос")
	mode := fs.String("mode", "auto", "auto: вектор первым, текст запасным; text; vector")
	limit := fs.Int("limit", memory.DefaultFindLimit, "сколько записей показать")
	threshold := fs.Float64("threshold", memory.DefaultFindThreshold, "порог близости смыслового слоя")
	url := fs.String("embed-url", defaultEmbedURL, "адрес службы эмбеддингов (протокол Ollama)")
	model := fs.String("embed-model", defaultEmbedModel, "модель эмбеддингов")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if strings.TrimSpace(*q) == "" {
		fmt.Fprintln(stderr, "memory find: нужен --q")
		return 2
	}
	m, err := memory.ParseFindMode(*mode)
	if err != nil {
		return memoryFail(stderr, err)
	}
	res, err := memoryEmbedService(dir, *url, *model, memory.Limits{}).Find(*q, memory.FindOptions{Mode: m, Limit: *limit, Threshold: *threshold})
	if err != nil {
		return memoryFail(stderr, err)
	}
	if len(res.Hits) == 0 {
		fmt.Fprintf(stdout, "memory: ничего не найдено (слой: %s)\n", res.Via)
	}
	for _, h := range res.Hits {
		fmt.Fprintf(stdout, "%s\t%.3f\t%s\t%s\t%s\n", res.Via, h.Score, h.Note.Name, h.Note.Topic, h.Note.Description)
	}
	if res.Notice != "" {
		fmt.Fprintf(stdout, "⚠️ %s\n", res.Notice)
	}
	return 0
}
