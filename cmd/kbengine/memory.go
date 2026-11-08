package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

const memoryUsage = "usage: kbengine memory <init|check|add> [flags] <dir>"

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
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet("memory "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var limit int
	var name, desc, body string
	switch verb {
	case "init":
	case "check":
		fs.IntVar(&limit, "hot-limit", domain.DefaultHotLimit, "потолок горячего слоя в знаках")
	case "add":
		fs.StringVar(&name, "name", "", "имя записи: латиница, цифры, дефисы")
		fs.StringVar(&desc, "description", "", "одна строка до 150 знаков — она же крючок в индексе")
		fs.StringVar(&body, "body", "", "текст записи")
	default:
		fmt.Fprintf(stderr, "memory: неизвестная подкоманда %q\n%s\n", verb, memoryUsage)
		return 2
	}
	if code, stop := parseFlags(fs, rest); stop {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, memoryUsage)
		return 2
	}
	dir := fs.Arg(0)
	svc := memory.New(memorydir.New(dir), limit)

	switch verb {
	case "init":
		if err := svc.Init(); err != nil {
			fmt.Fprintf(stderr, "memory: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "memory: скелет в %s (HOT.md, cold/INDEX.md)\n", dir)
	case "check":
		ps, err := svc.Check()
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
	case "add":
		if err := svc.Add(name, desc, body); err != nil {
			fmt.Fprintf(stderr, "memory: %v\n", err)
			if errors.Is(err, domain.ErrInvalidNote) {
				return 2
			}
			return 1
		}
		fmt.Fprintf(stdout, "memory: добавлено %s.md и строка в индексе\n", name)
	}
	return 0
}
