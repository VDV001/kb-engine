package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func txParams() domain.TransactionParams {
	return domain.TransactionParams{
		ID:          "01JQ0000000000000000000000",
		Kind:        "expense",
		Date:        time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(50000),
		Category:    "Еда",
		Subcategory: "Рестораны/кафе",
		Place:       "Поль Бейкери",
		Description: "Кафе",
		Source:      "Чек",
		Now:         func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) },
	}
}

func TestNewTransaction_valid(t *testing.T) {
	tx, err := domain.NewTransaction(txParams())
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if !tx.IsExpense() {
		t.Error("IsExpense() = false, want true")
	}
	if got := tx.Amount().Kopecks(); got != 50000 {
		t.Errorf("Amount = %d kopecks, want 50000", got)
	}
	// An expense must read as negative when summed into a balance, regardless of
	// how the ledger stores it — the sheet keeps expenses as positive numbers.
	if got := tx.SignedAmount().Kopecks(); got != -50000 {
		t.Errorf("SignedAmount = %d kopecks, want -50000", got)
	}
}

func TestNewTransaction_incomeIsPositive(t *testing.T) {
	p := txParams()
	p.Kind = "income"
	// The expense-shaped fields are cleared, not only the category: Доходы has no
	// column for any of them, and this test is about the sign of the amount.
	p.Category, p.Subcategory, p.Place = "", "", ""
	p.Source = "Зарплата"
	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if tx.IsExpense() {
		t.Error("IsExpense() = true, want false")
	}
	if got := tx.SignedAmount().Kopecks(); got != 50000 {
		t.Errorf("SignedAmount = %d kopecks, want 50000", got)
	}
}

func TestNewTransaction_invariants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.TransactionParams)
		want   error
	}{
		{"empty id", func(p *domain.TransactionParams) { p.ID = "" }, domain.ErrInvalidTransaction},
		{"unknown kind", func(p *domain.TransactionParams) { p.Kind = "refund" }, domain.ErrInvalidTransaction},
		{"zero date", func(p *domain.TransactionParams) { p.Date = time.Time{} }, domain.ErrInvalidTransaction},
		{"zero amount", func(p *domain.TransactionParams) { p.Amount = domain.NewMoney(0) }, domain.ErrInvalidTransaction},
		{"expense without category", func(p *domain.TransactionParams) { p.Category = "" }, domain.ErrInvalidTransaction},
		{
			"date in the future",
			func(p *domain.TransactionParams) {
				p.Date = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
			},
			domain.ErrInvalidTransaction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := txParams()
			tt.mutate(&p)
			if _, err := domain.NewTransaction(p); !errors.Is(err, tt.want) {
				t.Errorf("NewTransaction() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// The clock is an input, not an ambient fact: the same ledger must validate
// identically on any machine and in any test run.
func TestNewTransaction_clockIsInjected(t *testing.T) {
	p := txParams()
	p.Date = time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if _, err := domain.NewTransaction(p); err == nil {
		t.Fatal("expected a future date to be rejected against the injected clock")
	}
	p.Now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := domain.NewTransaction(p); err != nil {
		t.Errorf("same date with a later clock should be accepted, got %v", err)
	}
}

// Categories come from a reference sheet that has already drifted from the
// data: the sheet lists 8, the ledger uses 12 (Долги, Интернет, Внешний вид,
// Банк appear only in the rows). A closed enum would reject real history, so
// the invariant is "present and normalized", not "member of a fixed set".
func TestNewTransaction_categoryIsOpenButNormalized(t *testing.T) {
	p := txParams()
	p.Category = "  Долги  "
	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("category outside the reference sheet must be accepted: %v", err)
	}
	if tx.Category() != "Долги" {
		t.Errorf("Category = %q, want %q (trimmed)", tx.Category(), "Долги")
	}
}

// A transaction is dated by day, so "in the future" is a question about days
// and not about instants. Comparing the two mixes units, and the mistake only
// shows up east of UTC: at 02:41 in Yekaterinburg it is already the 29th while
// UTC still says the 28th, and an entry made then is not a future entry.
func TestNewTransaction_futureIsMeasuredInDays(t *testing.T) {
	yekaterinburg := time.FixedZone("YEKT", 5*60*60)
	tests := []struct {
		name       string
		date       time.Time
		now        time.Time
		wantReject bool
	}{
		{
			name: "today, clock later the same day",
			date: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
			now:  time.Date(2026, 7, 29, 21, 41, 0, 0, time.UTC),
		},
		{
			name: "today east of UTC, where UTC is still yesterday",
			date: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
			now:  time.Date(2026, 7, 29, 2, 41, 0, 0, yekaterinburg),
		},
		{
			name:       "tomorrow",
			date:       time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
			now:        time.Date(2026, 7, 29, 2, 41, 0, 0, yekaterinburg),
			wantReject: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := txParams()
			p.Date = tt.date
			p.Now = func() time.Time { return tt.now }
			_, err := domain.NewTransaction(p)
			if tt.wantReject && !errors.Is(err, domain.ErrInvalidTransaction) {
				t.Errorf("NewTransaction() error = %v, want it rejected", err)
			}
			if !tt.wantReject && err != nil {
				t.Errorf("NewTransaction() = %v, want it accepted", err)
			}
		})
	}
}

// The account is which of your banks the money moved through. Open like the
// category: the workbook names five accounts on its Счета sheet, and a closed
// enum would reject a sixth the day one is opened.
func TestNewTransaction_account(t *testing.T) {
	p := txParams()
	p.Account = "  Сбербанк  "
	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if got := tx.Account(); got != "Сбербанк" {
		t.Errorf("Account = %q, want it trimmed", got)
	}

	// Absent is legitimate: 32 of the 507 rows in the real ledger record no
	// account at all, and refusing them would refuse the user's history.
	p.Account = ""
	if _, err := domain.NewTransaction(p); err != nil {
		t.Errorf("a transaction without an account must be accepted: %v", err)
	}
}

// A transaction read from a spreadsheet carries a positional id ("expense-r42")
// that stops being meaningful the moment a row is inserted above it. On first
// import it is given a stable one, and everything else about the transaction
// must survive the swap untouched.
func TestTransaction_WithID(t *testing.T) {
	tx, err := domain.NewTransaction(txParams())
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}

	renamed, err := tx.WithID("  01JQ0000000000000000000001  ")
	if err != nil {
		t.Fatalf("WithID: %v", err)
	}
	if got := renamed.ID(); got != "01JQ0000000000000000000001" {
		t.Errorf("ID = %q, want the trimmed new id", got)
	}
	if renamed.Category() != tx.Category() || renamed.Amount() != tx.Amount() || !renamed.Date().Equal(tx.Date()) {
		t.Error("WithID changed a field other than the id")
	}
	// The receiver is a value: the original must still carry its own id, or two
	// records built from one row would silently share an identity.
	if tx.ID() != txParams().ID {
		t.Errorf("original ID = %q, want it unchanged", tx.ID())
	}

	if _, err := tx.WithID("   "); !errors.Is(err, domain.ErrInvalidTransaction) {
		t.Errorf("WithID(blank) error = %v, want ErrInvalidTransaction", err)
	}
}

// The real ledger records refunds as negative expenses ("Продажа игры
// (возврат)", −5500, twice in April). Rejecting them would make the engine
// refuse the user's actual history, so a negative expense is valid and must
// increase the balance — the sign flips exactly once, in SignedAmount.
func TestNewTransaction_refundIsANegativeExpense(t *testing.T) {
	p := txParams()
	p.Amount = domain.NewMoney(-550000)
	p.Description = "Продажа игры (возврат)"

	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("a refund must be accepted: %v", err)
	}
	if got := tx.SignedAmount().Kopecks(); got != 550000 {
		t.Errorf("refund SignedAmount = %d kopecks, want +550000 (money came back)", got)
	}
}
