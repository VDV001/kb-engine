package financexlsx_test

import (
	"os"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// Reads the real ledger and reports what the engine sees, so the numbers can be
// compared against the Python build. Skipped unless LEDGER points at a workbook,
// because the file is personal and lives outside the repository.
//
//	LEDGER=~/claude-cowork/finances/Учёт_финансов.xlsx go test ./internal/adapter/financexlsx/ -run Live -v
func TestLive_realLedger(t *testing.T) {
	path := os.Getenv("LEDGER")
	if path == "" {
		t.Skip("set LEDGER=<path to Учёт_финансов.xlsx> to run")
	}
	led, err := financexlsx.Read(path, time.Now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var exp, inc int
	var expSum, incSum domain.Money
	for _, tx := range led.Transactions {
		if tx.IsExpense() {
			exp, expSum = exp+1, expSum.Add(tx.Amount())
		} else {
			inc, incSum = inc+1, incSum.Add(tx.Amount())
		}
	}
	t.Logf("expenses=%d sum=%s", exp, expSum)
	t.Logf("income=%d sum=%s", inc, incSum)
	t.Logf("accounts=%d total=%s", len(led.Accounts), led.TotalBalance())
	for _, a := range led.Accounts {
		t.Logf("  %-14s %14s  %s", a.Bank(), a.Balance(), a.Updated().Format(time.DateOnly))
	}
}
