package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/adapter/financevocab"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// runFinSpelling печатает места и счета, записанные больше чем одним способом.
//
// Живёт под `fin`, а не под `audit --check spelling`, как предлагала задача:
// у `audit` обязателен --catalog, а этой проверке каталог не нужен вовсе —
// она читает журнал и словарь. Требовать флаг, который не используется, значит
// учить передавать его не глядя.
func runFinSpelling(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin spelling", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin spelling: --ledger is required")
		return 2
	}

	recs, err := financejsonl.Load(*ledgerPath, time.Now)
	if err != nil {
		fmt.Fprintf(stderr, "fin spelling: %v\n", err)
		return 1
	}

	// Словарь необязателен, но его отсутствие называется вслух: без него
	// проверка не увидит канон, ведущий на написание, которого в журнале нет,
	// — а это самый тревожный из трёх признаков, потому что словарь ПИШЕТ.
	vocPath := financevocab.PathNextTo(*ledgerPath)
	voc, vocErr := financevocab.Load(vocPath)
	if vocErr != nil {
		fmt.Fprintf(stderr, "fin spelling: словарь %s не прочитан (%v) — проверка канона не выполнялась\n",
			vocPath, vocErr)
	}

	findings := finance.SpellingIssues(recs, voc)
	if len(findings) == 0 {
		// «Расхождений не найдено» и молчание — разные ответы: молчание
		// неотличимо от непрогнанной проверки.
		fmt.Fprintf(stdout, "fin spelling: расхождений не найдено (записей проверено: %d)\n", len(recs))
		return 0
	}

	fmt.Fprintf(stdout, "fin spelling: расхождений — %d (записей проверено: %d)\n\n", len(findings), len(recs))
	for _, f := range findings {
		fmt.Fprintf(stdout, "%s — %s\n", f.Kind, f.Reason)
		for _, form := range f.Forms {
			switch {
			case form.FromVocabulary:
				fmt.Fprintf(stdout, "    %-28s ← словарь пишет так, в журнале ноль записей\n", form.Value)
			default:
				fmt.Fprintf(stdout, "    %-28s %d\n", form.Value, form.Count)
			}
		}
		fmt.Fprintln(stdout)
	}
	// Не ошибка: расхождение — это вопрос человеку, а не поломка. Код 1 сделал
	// бы проверку непригодной для привычки запускать её между делом.
	return 0
}
