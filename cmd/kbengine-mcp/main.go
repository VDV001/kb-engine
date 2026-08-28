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

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
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

	srv := mcpserver.New(svc, search.New(syn), version, *viewBase)
	// Диагностика уходит в stderr намеренно: stdout занят протоколом, и любая
	// лишняя строка там ломает разбор JSON-RPC у клиента.
	fmt.Fprintf(stderr, "kbengine-mcp %s: каталог %s, инструменты search_catalog · get_entry · stats\n", version, *catalogPath)
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}
