package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// runFinDelete removes one entry from the ledger.
//
// It exists because the only way to undo a mistyped expense was to open
// transactions.jsonl and cut the line by hand — that is, to walk around the
// engine. That door is exactly how a row without an id once got into the
// workbook, after which `fin sync` refused to run at all (#201). A rule that
// cannot be followed is broken not out of laziness.
//
// Deleting money asks first. The entry is printed in full and the question is
// asked about THAT entry: ids are typed by hand, they differ by one character,
// and the neighbouring row is someone else's real purchase.
func runFinDelete(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	id := fs.String("id", "", "id of the entry to delete")
	yes := fs.Bool("yes", false, "delete without asking (for non-interactive use)")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin delete: --ledger is required")
		return 2
	}
	if *id == "" {
		fmt.Fprintln(stderr, "fin delete: --id is required")
		return 2
	}

	// Найти и показать — до всякой записи. Подтверждать «id такой-то» нельзя:
	// человек подтверждает трату, а не строку идентификатора.
	rec, err := findInLedger(*ledgerPath, *id)
	if err != nil {
		fmt.Fprintf(stderr, "fin delete: %v\n", err)
		return 1
	}
	tx := rec.Transaction()
	fmt.Fprintf(stdout, "fin delete: %s  %s  %10s  %s %s %s\n",
		tx.ID(), tx.Date().Format(time.DateOnly), tx.Amount(),
		tx.Category(), tx.Description(), tx.Account())

	if !*yes && !confirmed(stdin, stdout) {
		fmt.Fprintln(stdout, "fin delete: не удалено")
		return 0
	}

	if err := deleteFromLedger(*ledgerPath, *id); err != nil {
		fmt.Fprintf(stderr, "fin delete: %v\n", err)
		return 1
	}
	// Строку в книге чистит обычный синк — он это уже умеет, и второй путь к
	// одному и тому же однажды разошёлся бы с первым.
	fmt.Fprintf(stdout, "fin delete: удалена %s — строку в книге очистит `fin sync`\n", *id)
	return 0
}

// confirmed asks and reads one line. Anything but a plain yes is a no: the
// default has to be the harmless one, because a person who hits Enter out of
// habit means "wait", not "delete".
func confirmed(stdin io.Reader, stdout io.Writer) bool {
	fmt.Fprint(stdout, "удалить эту запись? [y/N] ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "д", "да":
		return true
	default:
		return false
	}
}

// findInLedger returns the entry without holding the lock: nothing is written
// yet, and the person is about to be asked a question that can take minutes.
// Holding a write lock across an interactive prompt would block every other
// surface for as long as the terminal is unattended.
func findInLedger(ledgerPath, id string) (finance.Record, error) {
	recs, err := financejsonl.Load(ledgerPath, time.Now)
	if err != nil {
		return finance.Record{}, err
	}
	for _, rec := range recs {
		if rec.Transaction().ID() == id {
			return rec, nil
		}
	}
	return finance.Record{}, fmt.Errorf("%w: %s", ErrNoSuchEntry, id)
}

// deleteFromLedger removes the entry under the lock, re-reading the file: the
// answer to the question was given against what was on screen, and between the
// question and the write another surface may have appended a row.
//
// The record is looked up again rather than trusted from before for the same
// reason — and if it is gone by now, that is not an error to report as a
// failure of this command, it is the same outcome the person asked for.
func deleteFromLedger(ledgerPath, id string) error {
	return filelock.With(ledgerPath, func() error {
		recs, err := financejsonl.Load(ledgerPath, time.Now)
		if err != nil {
			return err
		}
		kept := make([]finance.Record, 0, len(recs))
		found := false
		for _, rec := range recs {
			if rec.Transaction().ID() == id {
				found = true
				continue
			}
			kept = append(kept, rec)
		}
		if !found {
			return fmt.Errorf("%w: %s", ErrNoSuchEntry, id)
		}
		// Порядок остальных не трогается: сортировать файл здесь значило бы
		// смешать удаление с перестановкой строк, и диff перестал бы отвечать на
		// вопрос «что именно убрали».
		return financejsonl.Save(ledgerPath, kept, time.Now)
	})
}
