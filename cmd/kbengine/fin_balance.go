package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// runFinBalance records a new balance for one account on the workbook's Счета
// sheet.
//
// Balances live in the workbook rather than the ledger: they are a snapshot of
// what a bank says today, not a transaction, and nothing in the ledger produces
// them. That is why this command takes --from and not --ledger.
func runFinBalance(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin balance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "path to Учёт_финансов.xlsx")
	bank := fs.String("bank", "", "account name as the Счета sheet spells it")
	amount := fs.String("amount", "", "new balance, e.g. 4321,55")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	for _, req := range []struct{ flag, value string }{
		{"--from", *from}, {"--bank", *bank}, {"--amount", *amount},
	} {
		if req.value == "" {
			fmt.Fprintf(stderr, "fin balance: %s is required\n", req.flag)
			return 2
		}
	}

	// ParseMoney, not MoneyFromFloat: this is text a person typed, so more
	// precision than a kopeck is a typo and gets reported rather than rounded.
	money, err := domain.ParseMoney(*amount)
	if err != nil {
		fmt.Fprintf(stderr, "fin balance: --amount %q: %v\n", *amount, err)
		return 1
	}

	if err := financexlsx.SetBalance(*from, *bank, money, time.Now); err != nil {
		fmt.Fprintf(stderr, "fin balance: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "%s: %s (%s)\n", *bank, money, time.Now().Format(time.DateOnly))
	return 0
}
