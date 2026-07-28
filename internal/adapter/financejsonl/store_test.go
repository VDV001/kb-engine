package financejsonl_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

var (
	writtenAt = time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	// loadedAt is the clock a load is checked against. Later than writtenAt, the
	// way a real load always is.
	loadedAt = func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }
)

func record(t *testing.T, id string, p domain.TransactionParams) finance.Record {
	t.Helper()
	p.ID = id
	p.Now = func() time.Time { return writtenAt }
	tx, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	rec, err := finance.NewRecord(tx, 1, writtenAt)
	if err != nil {
		t.Fatalf("build record: %v", err)
	}
	return rec
}

func expense(t *testing.T, id string) finance.Record {
	t.Helper()
	return record(t, id, domain.TransactionParams{
		Kind:        domain.KindExpense,
		Date:        time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(20245),
		Category:    "Еда",
		Subcategory: "Продукты",
		Place:       "Пятёрочка",
		Description: "хлеб & молоко <2 шт>",
		Source:      "Чек",
	})
}

func TestSaveLoad_roundTripsEveryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	income := record(t, "01B", domain.TransactionParams{
		Kind:   domain.KindIncome,
		Date:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount: domain.NewMoney(9000000),
		Source: "Зарплата",
	})
	want := []finance.Record{expense(t, "01A"), income}

	if err := financejsonl.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := financejsonl.Load(path, loadedAt)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d records, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i].Transaction(), got[i].Transaction()
		if g.ID() != w.ID() || g.Kind() != w.Kind() || !g.Date().Equal(w.Date()) ||
			g.Amount() != w.Amount() || g.Category() != w.Category() ||
			g.Subcategory() != w.Subcategory() || g.Place() != w.Place() ||
			g.Description() != w.Description() || g.Source() != w.Source() {
			t.Errorf("record %d did not round-trip:\n got %+v\nwant %+v", i, g, w)
		}
		if got[i].Rev() != want[i].Rev() || !got[i].UpdatedAt().Equal(want[i].UpdatedAt()) {
			t.Errorf("record %d revision metadata lost: rev=%d updatedAt=%s",
				i, got[i].Rev(), got[i].UpdatedAt())
		}
	}
}

// The file is meant to be read and diffed by a person, so the amount is written
// the way it is spoken and the text stays legible: no HTML escaping of & and <,
// no \u04xx for Cyrillic.
func TestSave_writesReadableLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	if err := financejsonl.Save(path, []finance.Record{expense(t, "01A")}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	line := string(raw)
	for _, want := range []string{`"amount":"202.45"`, `"date":"2026-03-29"`, `"Еда"`, `&`, `<2 шт>`} {
		if !strings.Contains(line, want) {
			t.Errorf("written line is missing %s:\n%s", want, line)
		}
	}
	if strings.Count(strings.TrimRight(line, "\n"), "\n") != 0 {
		t.Errorf("one record must be exactly one line, got:\n%s", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Error("file must end with a newline so appending stays safe")
	}
}

// Empty optional fields are left out rather than written as "": an income row
// has no category or place, and carrying five empty strings per line makes the
// file harder to read for no gain.
func TestSave_omitsEmptyOptionalFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	income := record(t, "01B", domain.TransactionParams{
		Kind:   domain.KindIncome,
		Date:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount: domain.NewMoney(9000000),
		Source: "Зарплата",
	})
	if err := financejsonl.Save(path, []finance.Record{income}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, _ := os.ReadFile(path)
	for _, unwanted := range []string{`"category"`, `"place"`, `"subcategory"`, `"description"`} {
		if strings.Contains(string(raw), unwanted) {
			t.Errorf("empty %s should not be written:\n%s", unwanted, raw)
		}
	}
}

// Replacing the file must go through a temp file and a rename, so a crash
// mid-write cannot leave a truncated ledger. What is observable from outside is
// that the directory is clean afterwards and the old contents are fully gone.
func TestSave_replacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")
	if err := financejsonl.Save(path, []finance.Record{expense(t, "01A"), expense(t, "01B")}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := financejsonl.Save(path, []finance.Record{expense(t, "01C")}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := financejsonl.Load(path, loadedAt)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Transaction().ID() != "01C" {
		t.Errorf("second Save did not fully replace the file: %d records", len(got))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp files left behind: %v", names)
	}
}

// A row is validated against the clock at load time, not against its own
// updated_at.
//
// Found by adding an entry at 02:43 local time east of UTC: the row was dated
// the 29th and stamped 2026-07-28T21:43Z, so checking it against its own
// timestamp made it a future entry that could never be read back. Time only
// ever makes a date less future, so a real clock cannot reject a row that was
// accepted when it was written.
func TestLoad_validatesAgainstTheClockNotTheRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	content := `{"id":"01A","kind":"expense","date":"2026-07-29","amount":"1500.50","category":"Еда","rev":1,"updated_at":"2026-07-28T21:43:21Z"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := financejsonl.Load(path, loadedAt); err != nil {
		t.Errorf("Load: %v — a row written today must survive being read back", err)
	}

	// It still fails closed on a date that is genuinely ahead of the clock.
	ahead := `{"id":"01B","kind":"expense","date":"2026-07-30","amount":"1.00","category":"Еда","rev":1,"updated_at":"2026-07-29T12:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(ahead), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := financejsonl.Load(path, loadedAt); !errors.Is(err, domain.ErrInvalidTransaction) {
		t.Errorf("Load() error = %v, want a tomorrow-dated row rejected", err)
	}
}

// Fail closed. A ledger that silently reports fewer transactions than it holds
// is worse than one that refuses to load: the first quietly changes every total.
func TestLoad_failsClosed(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    error
	}{
		{
			name:    "malformed json",
			content: `{"id":"01A","kind":"expense"` + "\n",
			want:    financejsonl.ErrMalformedLine,
		},
		{
			name: "duplicate id",
			content: `{"id":"01A","kind":"expense","date":"2026-03-29","amount":"1.00","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}` + "\n" +
				`{"id":"01A","kind":"expense","date":"2026-03-30","amount":"2.00","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}` + "\n",
			want: finance.ErrDuplicateID,
		},
		{
			name:    "amount finer than a kopeck",
			content: `{"id":"01A","kind":"expense","date":"2026-03-29","amount":"1.005","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}` + "\n",
			want:    domain.ErrInvalidMoney,
		},
		{
			name:    "violates a domain invariant",
			content: `{"id":"01A","kind":"expense","date":"2026-03-29","amount":"0.00","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}` + "\n",
			want:    domain.ErrInvalidTransaction,
		},
		{
			name:    "revision missing",
			content: `{"id":"01A","kind":"expense","date":"2026-03-29","amount":"1.00","category":"Еда","updated_at":"2026-07-29T02:00:00Z"}` + "\n",
			want:    finance.ErrInvalidRecord,
		},
		{
			name:    "unparsable date",
			content: `{"id":"01A","kind":"expense","date":"29.03.2026","amount":"1.00","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}` + "\n",
			want:    financejsonl.ErrMalformedLine,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transactions.jsonl")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := financejsonl.Load(path, loadedAt); !errors.Is(err, tt.want) {
				t.Errorf("Load() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// The line number is the whole point of the error: a 580-line ledger with one
// bad row is fixed in seconds if the message says which row.
func TestLoad_reportsTheOffendingLine(t *testing.T) {
	good := func(id string) string {
		return `{"id":"` + id + `","kind":"expense","date":"2026-03-29","amount":"1.00","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}`
	}
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	content := good("01A") + "\n" + good("01B") + "\n" + `{"id":` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := financejsonl.Load(path, loadedAt)
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("Load() error = %v, want it to name line 3", err)
	}
}

// A missing ledger is an error, not an empty one. Reporting zero transactions
// because the file was not where it was expected is exactly the failure mode
// that overwrote the finances on 2026-07-28.
func TestLoad_missingFileIsAnError(t *testing.T) {
	_, err := financejsonl.Load(filepath.Join(t.TempDir(), "nope.jsonl"), loadedAt)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load() error = %v, want it to wrap os.ErrNotExist", err)
	}
}

func TestLoad_ignoresBlankLines(t *testing.T) {
	good := `{"id":"01A","kind":"expense","date":"2026-03-29","amount":"1.00","category":"Еда","rev":1,"updated_at":"2026-07-29T02:00:00Z"}`
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	if err := os.WriteFile(path, []byte("\n"+good+"\n\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := financejsonl.Load(path, loadedAt)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("loaded %d records, want 1", len(got))
	}
}
