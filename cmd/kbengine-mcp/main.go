// Команда kbengine-mcp отдаёт каталог базы знаний агентам по протоколу MCP.
//
// Отдельным бинарём, а не подкомандой kbengine, по замеру: SDK протокола весит
// +4 МБ, а stdio-сервер бессмысленен внутри контейнера с витриной. Основной
// бинарь этот пакет не импортирует и не растёт от него ни на байт — проверено
// сборкой обоих артефактов, а не выведено из устройства линковщика.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
	"github.com/daniil/kb-engine/internal/adapter/searchsyn"
	"github.com/daniil/kb-engine/internal/usecase/query"
	"github.com/daniil/kb-engine/internal/usecase/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version подставляется линковщиком при релизной сборке, как у kbengine.
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "kbengine-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("kbengine-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalogPath := fs.String("catalog", "", "путь к catalog.json (обязателен)")
	showVersion := fs.Bool("version", false, "напечатать версию и выйти")
	// Адрес витрины задаётся снаружи, потому что порт выбирается при запуске
	// serve и на чужой машине он другой. Умолчания нет намеренно: без флага
	// поле view приходит пустым, и это честнее ссылки в никуда.
	viewBase := fs.String("view-base", "", "адрес витрины для поля view, например http://127.0.0.1:8097")
	// Поиск по файлам включается явно: сервер над каталогом не должен сам
	// решать, что агенту можно читать диск владельца.
	filesConfig := fs.String("files-config", "", "config.json окна kb-desktop: включает search_files и read_file по выбранным в нем папкам")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintln(stderr, version)
		return nil
	}
	if *catalogPath == "" {
		return fmt.Errorf("нужен --catalog: без каталога отвечать нечем")
	}

	loader := catalogjson.FileLoader{Path: *catalogPath}
	svc := query.NewService(loader)
	// Каталог читается СРАЗУ, а не при первом запросе: агент подключается
	// молча, и «сервер поднялся» с нечитаемым каталогом выглядело бы как
	// рабочее состояние вплоть до первого пустого ответа.
	if _, err := svc.Stats(); err != nil {
		return fmt.Errorf("каталог не читается: %w", err)
	}

	// Словарь необязателен, но его отсутствие называется вслух — тем же
	// сообщением, что у витрины и терминала. Молча оставшись без слоя перевода,
	// поиск выглядел бы просто плохим.
	syn, err := searchsyn.Load(searchsyn.PathNextTo(*catalogPath))
	if err != nil {
		fmt.Fprintf(stderr, "kbengine-mcp: %v — поиск ищет подстрокой, транслитерацией и с опечатками, но не переводит термины\n", err)
	}

	// Журнал вызовов — тот же файл, что у команд движка; путь спрашивается у
	// того же места (KBENGINE_RUNLOG или XDG-каталог состояния), иначе счётчик
	// писал бы в один файл, а `kbengine runs` читал другой.
	//
	// Ненайденный путь не отменяет работу сервера: отвечать агенту важнее, чем
	// считать вызовы. Но и молчать нельзя — пустой журнал неотличим от
	// «вызовов не было».
	var recorder mcpserver.Recorder
	journalPath, err := runlogjsonl.DefaultPath(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "kbengine-mcp: %v — вызовы не считаются\n", err)
	} else {
		recorder = newJournalRecorder(journalPath, time.Now, stderr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Перед выходом сборка индекса отменяется и дожидается: иначе процесс
	// умирал посреди записи снимка, оставляя временный файл в ~100 МБ (ревью #375).
	var bg sync.WaitGroup
	defer func() { cancel(); bg.Wait() }()
	cfg := mcpserver.Config{Version: version, ViewBase: *viewBase, Recorder: recorder}
	tools := "search_catalog · get_entry · stats"
	if *filesConfig != "" {
		files, err := newFileIndex(*filesConfig, userHome(), stderr)
		if err != nil {
			return err
		}
		bg.Go(func() { files.run(ctx) })
		cfg.Files = files.svc
		tools += " · search_files · read_file"
	}
	srv := mcpserver.New(svc, search.New(syn), cfg)
	// Диагностика уходит в stderr намеренно: stdout занят протоколом, и любая
	// лишняя строка там ломает разбор JSON-RPC у клиента.
	fmt.Fprintf(stderr, "kbengine-mcp %s: каталог %s, инструменты %s\n", version, *catalogPath, tools)
	if recorder != nil {
		fmt.Fprintf(stderr, "kbengine-mcp: вызовы пишутся в %s, счёт печатает `kbengine runs`\n", journalPath)
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}
