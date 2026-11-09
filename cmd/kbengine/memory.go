package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/embedhttp"
	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/adapter/portraitfile"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
	"github.com/daniil/kb-engine/internal/usecase/portrait"
)

const memoryUsage = "usage: kbengine memory <init|check|add|topics|pending|approve|reject|index|find|who|seal|health|review|confirm> [flags] [dir]  (dir по умолчанию: ~/Library/Application Support/kb-desktop/dvk-memory)"

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
	"who":     memoryWho,
	"seal":    memorySeal,
	"health":  memoryHealth,
	"review":  memoryReview,
	"confirm": memoryConfirmRule,
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
	hot := fs.Int("hot-limit", 0, "потолок горячего слоя в знаках (по умолчанию 4000); запоминается в memory.json")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if err := domain.ValidateLimits(*hot, 0); err != nil { // до скелета: отказ не оставляет полупустую папку
		return memoryFail(stderr, err)
	}
	st := memorydir.New(dir)
	if err := memory.New(st, memory.Limits{}).Init(); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	if *hot > 0 {
		if err := st.SaveLimits(memory.Limits{Hot: *hot}); err != nil {
			return memoryFail(stderr, err)
		}
	}
	fmt.Fprintf(stdout, "memory: скелет в %s (HOT.md, cold/INDEX.md, journal/)\n", dir)
	return 0
}

// memoryLimits собирает потолки: флаг, если задан, иначе memory.json этой памяти,
// иначе умолчания сценария (нули).
func memoryLimits(st *memorydir.Store, hotFlag, indexFlag int) (memory.Limits, error) {
	l, err := st.Limits()
	if err != nil {
		return l, err
	}
	if hotFlag > 0 {
		l.Hot = hotFlag
	}
	if indexFlag > 0 {
		l.Index = indexFlag
	}
	return l, nil
}

func memoryCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("hot-limit", 0, "потолок горячего слоя в знаках (0 — из memory.json, иначе 4000)")
	idxLimit := fs.Int("index-limit", 0, "потолок cold/INDEX.md в знаках (0 — из memory.json, иначе 30000)")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	st := memorydir.New(dir)
	lim, err := memoryLimits(st, *limit, *idxLimit)
	if err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	ps, err := memory.New(st, lim).Check()
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
	if errors.Is(err, domain.ErrInvalidNote) || errors.Is(err, domain.ErrInvalidLimits) {
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
	st := memorydir.New(dir)
	lim, err := memoryLimits(st, 0, 0)
	if err != nil {
		return memoryFail(stderr, err)
	}
	if layer == domain.LayerHot {
		// Правило в горячем слое начнет действовать во всех сессиях агентов: имя
		// записи вводится руками, как подтверждение, что человек его прочитал.
		if err := memoryConfirm("Правило из записи "+*name+" начнет действовать во всех сессиях агентов. Введите имя записи для подтверждения: ", *name); err != nil {
			fmt.Fprintf(stderr, "memory: %v\n", err)
			return 1
		}
	}
	if err := memory.New(st, lim).Approve(*name, layer); err != nil {
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
	defaultEmbedURL   = memorydir.EmbedURL
	defaultEmbedModel = memorydir.EmbedModel
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

// portraitFolder returns the portrait folder: флаг, иначе папка из настроек окна.
// Портрет на kb-сервере отсюда не читается (ponytail: только папка; путь вверх —
// kbserver-клиент портрета, он уже есть в окне).
func portraitFolder(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cfg, err := desktopconfig.Read(desktopconfig.Path(home))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", err
	case cfg.Chat.MemoryServer != "":
		return "", errors.New("портрет лежит на kb-сервере, а memory who читает папку: укажите --portrait-dir")
	case cfg.Chat.MemoryDir != "":
		return cfg.Chat.MemoryDir, nil
	}
	return "", errors.New("папка портрета не выбрана: укажите --portrait-dir или выберите папку в окне («Чат и модель → Что DVK помнит обо мне»)")
}

// folderPortrait is the memory.PortraitSource over portrait.json in a folder.
type folderPortrait struct{ dir string }

func (f folderPortrait) Text() (string, error) {
	p, err := portraitfile.Store{Dir: f.dir}.Load(context.Background())
	if err != nil {
		return "", err
	}
	return portrait.Text(p.Approved()), nil
}

func memoryWho(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory who", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pdir := fs.String("portrait-dir", "", "папка портрета DVK (по умолчанию из настроек окна)")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	folder, err := portraitFolder(*pdir)
	if err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	changed, err := memory.New(memorydir.New(dir), memory.Limits{}).WithPortrait(folderPortrait{folder}).Who()
	if err != nil {
		return memoryFail(stderr, err)
	}
	if !changed {
		fmt.Fprintln(stdout, "memory: блок «Кто я» актуален")
		return 0
	}
	fmt.Fprintln(stdout, "memory: блок «Кто я» обновлен из портрета; одобренные факты теперь лежат в HOT.md, а его читают агенты")
	return 0
}

// errNotConfirmed — человек не подтвердил действие в терминале.
var errNotConfirmed = errors.New("не подтверждено")

// memoryConfirm asks the person to type want before a write that changes what every
// agent session reads. Переменная, чтобы тесты подменяли терминал.
var memoryConfirm = confirmOnTerminal(os.Stdin, os.Stderr)

// confirmOnTerminal returns a confirmation that works only on a real terminal and
// only with the exact word typed: агент, запущенный без терминала, и случайный Enter
// не проходят. ponytail: это жест, а не замок. Агент с доступом к псевдотерминалу
// может его изобразить; настоящая граница — MCP-инструменты памяти без approve и
// Touch ID в окне.
func confirmOnTerminal(in io.Reader, out io.Writer) func(prompt, want string) error {
	return func(prompt, want string) error {
		f, ok := in.(*os.File)
		if !ok {
			return fmt.Errorf("%w: нужен терминал, а ввод идет не из него", errNotConfirmed)
		}
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("%w: нужен терминал, а ввод идет не из него", errNotConfirmed)
		}
		fmt.Fprint(out, prompt)
		line, err := bufio.NewReader(f).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("%w: ответа нет", errNotConfirmed)
		}
		if strings.TrimSpace(line) != want {
			return fmt.Errorf("%w: введено не %q", errNotConfirmed, want)
		}
		return nil
	}
}

func memorySeal(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory seal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	const word = "подтверждаю"
	if err := memoryConfirm("HOT.md изменен не системой. Прочитайте его и, если все верно, введите «"+word+"»: ", word); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	if err := memory.New(memorydir.New(dir), memory.Limits{}).Seal(); err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintln(stdout, "memory: текущий HOT.md принят, подтверждение записано в journal/decisions.md")
	return 0
}
