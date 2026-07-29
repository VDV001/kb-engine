package financexlsx_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// The property every write rests on: a transaction put into the workbook comes
// back out unchanged. Three separate defects turned out to be one violation of
// it seen from three sides, and two rounds of fixes chased the sightings
// instead of the property.
//
// The table below enumerates the forms a row can take rather than the bugs that
// were found, so a form nobody has hit yet fails here before it reaches the
// book. Fields the sheet has no column for — a category on Доходы — are not
// part of the property and are left unset.

// rowForm is one shape a transaction can have on its way to a cell.
type rowForm struct {
	name    string
	kind    string
	account string
	source  string
}

func expenseForms() []rowForm {
	return []rowForm{
		{name: "account and source", kind: domain.KindExpense, account: "Т-Банк", source: "Чек"},
		{name: "account only", kind: domain.KindExpense, account: "Т-Банк"},
		{name: "source only", kind: domain.KindExpense, source: "Чек"},
		{name: "neither", kind: domain.KindExpense},
	}
}

func incomeForms() []rowForm {
	return []rowForm{
		{name: "source only", kind: domain.KindIncome, source: "Зарплата"},
		{name: "neither", kind: domain.KindIncome},
	}
}

// pairedByOlderEngine is the workbook as an earlier version of this engine left
// it: ids in the eighth column, which is the one an account uses when Источник
// is busy recording how the row was captured.
//
// Books in that state exist and say nothing about it — the rule that keeps the
// two columns apart was added after some had already been paired. The fixture
// has to carry that state because the owner's book does not: it was paired late
// enough to have ids in the ninth.
func pairedByOlderEngine(t *testing.T) string {
	t.Helper()
	path := workbookWithoutExtraColumn(t)

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	set := func(sheet, cell, v string) {
		t.Helper()
		if err := f.SetCellStr(sheet, cell, v); err != nil {
			t.Fatalf("%s!%s: %v", sheet, cell, err)
		}
	}
	set("Расходы", "H2", "id")
	set("Расходы", "H3", "01A")
	set("Расходы", "H4", "01B")
	set("Доходы", "E2", "id")
	set("Доходы", "E3", "01C")
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

// pairedThenMigrated is that same book after the migration that moves ids off
// the account's column. Writing to it has to be as safe as writing to a book
// this engine paired itself — a migration that leaves the book almost right is
// worth less than the refusal it replaces.
func pairedThenMigrated(t *testing.T) string {
	t.Helper()
	path := pairedByOlderEngine(t)
	if _, err := financexlsx.MigrateIDColumn(path, writeClock); err != nil {
		t.Fatalf("MigrateIDColumn: %v", err)
	}
	return path
}

// formTx builds a transaction of the given form, filling every field the form's
// sheet has a column for so the round trip has something to lose.
func formTx(t *testing.T, id string, form rowForm) domain.Transaction {
	t.Helper()
	p := domain.TransactionParams{
		ID:          id,
		Kind:        form.kind,
		Date:        time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		Amount:      domain.NewMoney(123456),
		Description: "проверка формы",
		Source:      form.source,
		Account:     form.account,
		Now:         writeClock,
	}
	if form.kind == domain.KindExpense {
		p.Category, p.Subcategory, p.Place = "Еда", "Продукты", "Пятёрочка"
	}
	out, err := domain.NewTransaction(p)
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

// assertSameContent compares everything about two transactions except identity.
// Reported field by field: "not equal" on a whole struct says a round trip lost
// something without saying what.
func assertSameContent(t *testing.T, want, got domain.Transaction) {
	t.Helper()
	for _, f := range []struct{ name, want, got string }{
		{"kind", want.Kind(), got.Kind()},
		{"date", want.Date().Format(time.DateOnly), got.Date().Format(time.DateOnly)},
		{"amount", want.Amount().String(), got.Amount().String()},
		{"category", want.Category(), got.Category()},
		{"subcategory", want.Subcategory(), got.Subcategory()},
		{"place", want.Place(), got.Place()},
		{"description", want.Description(), got.Description()},
		{"source", want.Source(), got.Source()},
		{"account", want.Account(), got.Account()},
	} {
		if f.want != f.got {
			t.Errorf("%s = %q after write→read, want %q", f.name, f.got, f.want)
		}
	}
}

func TestApplyRows_roundTripsEveryRowForm(t *testing.T) {
	books := []struct {
		name  string
		build func(*testing.T) string
	}{
		{"paired by this engine", paired},
		{"paired by an older engine, then migrated", pairedThenMigrated},
	}
	// Which row the form is written over matters as much as the form itself: a
	// row that already carries an account and a capture method is the one a
	// write has to be able to empty, and an untouched row is the case where the
	// disputed state is absent.
	targets := []struct{ name, id string }{
		{"over an empty row", "01A"},
		{"over a row that already has both", "01B"},
	}

	for _, book := range books {
		for _, target := range targets {
			for _, form := range expenseForms() {
				t.Run(book.name+"/"+target.name+"/"+form.name, func(t *testing.T) {
					path := book.build(t)
					want := formTx(t, target.id, form)
					if err := financexlsx.ApplyRows(path,
						[]domain.Transaction{want}, nil, writeClock); err != nil {
						t.Fatalf("ApplyRows: %v", err)
					}
					got, ok := readBack(t, path)[target.id]
					if !ok {
						t.Fatalf("row %s is gone from the workbook after the write", target.id)
					}
					assertSameContent(t, want, got)
				})
			}
		}
		for _, form := range incomeForms() {
			t.Run(book.name+"/income/"+form.name, func(t *testing.T) {
				path := book.build(t)
				want := formTx(t, "01C", form)
				if err := financexlsx.ApplyRows(path,
					[]domain.Transaction{want}, nil, writeClock); err != nil {
					t.Fatalf("ApplyRows: %v", err)
				}
				got, ok := readBack(t, path)["01C"]
				if !ok {
					t.Fatal("income row 01C is gone from the workbook after the write")
				}
				assertSameContent(t, want, got)
			})
		}
	}
}
