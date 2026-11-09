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

// memoryVerbs — подкоманды `kbengine memory`; у каждой свой набор флагов.
var memoryVerbs = map[string]func(args []string, stdout, stderr io.Writer) int{
	"init":  memoryInit,
	"check": memoryCheck,
	"add":   memoryAdd,
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

// memoryDirArg разбирает флаги подкоманды и возвращает папку памяти.
func memoryDirArg(fs *flag.FlagSet, args []string, stderr io.Writer) (dir string, code int, stop bool) {
	if code, stop := parseFlags(fs, args); stop {
		return "", code, true
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, memoryUsage)
		return "", 2, true
	}
	return fs.Arg(0), 0, false
}

func memoryInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if err := memory.New(memorydir.New(dir), 0).Init(); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "memory: скелет в %s (HOT.md, cold/INDEX.md)\n", dir)
	return 0
}

func memoryCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("hot-limit", domain.DefaultHotLimit, "потолок горячего слоя в знаках")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	ps, err := memory.New(memorydir.New(dir), *limit).Check()
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
	desc := fs.String("description", "", "одна строка до 150 знаков — она же крючок в индексе")
	body := fs.String("body", "", "текст записи")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if err := memory.New(memorydir.New(dir), 0).Add(*name, *desc, *body); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		if errors.Is(err, domain.ErrInvalidNote) {
			return 2
		}
		return 1
	}
	fmt.Fprintf(stdout, "memory: добавлено %s.md и строка в индексе\n", *name)
	return 0
}
