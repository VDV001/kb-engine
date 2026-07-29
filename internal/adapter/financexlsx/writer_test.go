package financexlsx_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/xuri/excelize/v2"
)

var writeClock = func() time.Time { return time.Date(2026, 7, 29, 3, 30, 0, 0, time.UTC) }

// workbookWithExtraColumn mirrors the real ledger's awkward shape: seven
// documented columns, plus an eighth the owner added by hand with no header.
// The live file has bank names there on 19 of 507 rows.
func workbookWithExtraColumn(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetSheetName("Sheet1", "Расходы"))
	for i, h := range []string{"Дата", "Категория", "Подкатегория", "Место", "Описание", "Сумма (₽)", "Источник"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		must(f.SetCellValue("Расходы", cell, h))
	}
	must(f.SetCellValue("Расходы", "A3", time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Расходы", "B3", "Еда"))
	must(f.SetCellValue("Расходы", "F3", 202.45))
	must(f.SetCellValue("Расходы", "A4", time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Расходы", "B4", "Транспорт"))
	must(f.SetCellValue("Расходы", "F4", 1500))
	// Exactly the live shape: the source column says the record came from a
	// receipt, and the bank went into the unlabelled column beside it.
	must(f.SetCellValue("Расходы", "G4", "Чек"))
	// The currency format the live sheet uses, so an appended row has something
	// worth inheriting.
	money, err := f.NewStyle(&excelize.Style{CustomNumFmt: new(`#,##0.00" ₽"`)})
	must(err)
	must(f.SetCellStyle("Расходы", "F3", "F4", money))
	// The undocumented eighth column, exactly as in the live workbook.
	must(f.SetCellValue("Расходы", "H4", "Сбербанк"))

	_, err = f.NewSheet("Доходы")
	must(err)
	for i, h := range []string{"Дата", "Источник", "Описание", "Сумма (₽)"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		must(f.SetCellValue("Доходы", cell, h))
	}
	must(f.SetCellValue("Доходы", "A3", time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Доходы", "B3", "Зарплата"))
	must(f.SetCellValue("Доходы", "D3", 90000))

	// Счета is the vocabulary that decides what counts as an account.
	_, err = f.NewSheet("Счета")
	must(err)
	must(f.SetCellValue("Счета", "A2", "Банк"))
	for i, bank := range []string{"Сбербанк", "Альфа-Банк", "Т-Банк"} {
		cell, _ := excelize.CoordinatesToCellName(1, i+3)
		must(f.SetCellValue("Счета", cell, bank))
		amount, _ := excelize.CoordinatesToCellName(2, i+3)
		must(f.SetCellValue("Счета", amount, 100))
		updated, _ := excelize.CoordinatesToCellName(3, i+3)
		must(f.SetCellValue("Счета", updated, time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)))
	}

	path := filepath.Join(t.TempDir(), "Учёт_финансов.xlsx")
	must(f.SaveAs(path))
	return path
}

func cellValue(t *testing.T, path, sheet, cell string) string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	v, err := f.GetCellValue(sheet, cell)
	if err != nil {
		t.Fatalf("read %s!%s: %v", sheet, cell, err)
	}
	return v
}

// The id column goes into the first genuinely free column, not the one after
// the documented header. The live workbook has an unlabelled eighth column with
// bank names in it, and writing over that would destroy data the engine does
// not even read.
func TestAssignIDs_writesIntoTheFirstFreeColumn(t *testing.T) {
	path := workbookWithExtraColumn(t)
	err := financexlsx.AssignIDs(path, map[string]string{
		"expense-r3": "01A",
		"expense-r4": "01B",
		"income-r3":  "01C",
	}, writeClock)
	if err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}

	if got := cellValue(t, path, "Расходы", "H4"); got != "Сбербанк" {
		t.Errorf("column H = %q, want the untouched bank name", got)
	}
	// Header in row 2, so the column explains itself to a person opening the file.
	if got := cellValue(t, path, "Расходы", "I2"); got != "id" {
		t.Errorf("Расходы!I2 = %q, want the id header", got)
	}
	if got := cellValue(t, path, "Расходы", "I3"); got != "01A" {
		t.Errorf("Расходы!I3 = %q, want 01A", got)
	}
	if got := cellValue(t, path, "Расходы", "I4"); got != "01B" {
		t.Errorf("Расходы!I4 = %q, want 01B", got)
	}
	// Доходы has nothing past D, so its id column lands at E.
	if got := cellValue(t, path, "Доходы", "E2"); got != "id" {
		t.Errorf("Доходы!E2 = %q, want the id header", got)
	}
	if got := cellValue(t, path, "Доходы", "E3"); got != "01C" {
		t.Errorf("Доходы!E3 = %q, want 01C", got)
	}
	// Nothing else moved.
	if got := cellValue(t, path, "Расходы", "B3"); got != "Еда" {
		t.Errorf("Расходы!B3 = %q, want Еда", got)
	}
}

// The id column must clear the sheet's documented columns too, not just the
// cells that happen to be filled.
//
// Источник is the seventh column of Расходы and is often empty, which makes it
// look free. Writing ids there is invisible until something reads the sheet
// back and finds a ULID where the source of the record should be.
func TestAssignIDs_skipsDocumentedColumnsThatLookEmpty(t *testing.T) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// A sheet whose rightmost filled cell is the amount in F: columns C, D, E and
	// G carry nothing at all.
	must(f.SetSheetName("Sheet1", "Расходы"))
	must(f.SetCellValue("Расходы", "A2", "Дата"))
	must(f.SetCellValue("Расходы", "A3", time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Расходы", "B3", "Еда"))
	must(f.SetCellValue("Расходы", "F3", 202.45))
	_, err := f.NewSheet("Доходы")
	must(err)
	must(f.SetCellValue("Доходы", "A2", "Дата"))
	path := filepath.Join(t.TempDir(), "Учёт_финансов.xlsx")
	must(f.SaveAs(path))

	if err := financexlsx.AssignIDs(path, map[string]string{"expense-r3": "01A"}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}
	if got := cellValue(t, path, "Расходы", "G3"); got != "" {
		t.Errorf("Расходы!G3 = %q — the id landed in Источник", got)
	}

	led, readErr := financexlsx.Read(path, writeClock)
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if len(led.Transactions) != 1 {
		t.Fatalf("read %d transactions, want 1", len(led.Transactions))
	}
	if src := led.Transactions[0].Source(); src != "" {
		t.Errorf("Source = %q, want empty — the id was read back as the source", src)
	}
}

// Run twice, the same column is reused rather than a second one appearing next
// to it.
func TestAssignIDs_isIdempotent(t *testing.T) {
	path := workbookWithExtraColumn(t)
	assign := map[string]string{"expense-r3": "01A", "expense-r4": "01B", "income-r3": "01C"}
	for i := range 2 {
		if err := financexlsx.AssignIDs(path, assign, writeClock); err != nil {
			t.Fatalf("AssignIDs run %d: %v", i+1, err)
		}
	}
	if got := cellValue(t, path, "Расходы", "J2"); got != "" {
		t.Errorf("Расходы!J2 = %q, want empty — a second id column was added", got)
	}
	if got := cellValue(t, path, "Расходы", "I3"); got != "01A" {
		t.Errorf("Расходы!I3 = %q, want 01A", got)
	}
}

// LibreOffice keeps the workbook open for hours at a time and leaves a lock
// file next to it. Writing under that produces two divergent versions, one of
// which is overwritten without warning when the editor saves.
func TestAssignIDs_refusesALockedWorkbook(t *testing.T) {
	path := workbookWithExtraColumn(t)
	lock := filepath.Join(filepath.Dir(path), ".~lock."+filepath.Base(path)+"#")
	if err := os.WriteFile(lock, []byte("daniil"), 0o600); err != nil {
		t.Fatalf("create lock: %v", err)
	}
	err := financexlsx.AssignIDs(path, map[string]string{"expense-r3": "01A"}, writeClock)
	if !errors.Is(err, financexlsx.ErrWorkbookLocked) {
		t.Errorf("AssignIDs() error = %v, want ErrWorkbookLocked", err)
	}
	if got := cellValue(t, path, "Расходы", "I3"); got != "" {
		t.Error("a refused write must leave the workbook untouched")
	}
}

// The workbook is four years of hand-kept records and there is no git history
// behind it. Every write keeps a copy first.
func TestAssignIDs_backsUpBeforeWriting(t *testing.T) {
	path := workbookWithExtraColumn(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if err := financexlsx.AssignIDs(path, map[string]string{"expense-r3": "01A"}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}

	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".backup", "*.xlsx"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("got %d backups, want 1 (err %v)", len(backups), err)
	}
	saved, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(saved) != string(original) {
		t.Error("the backup is not a byte-for-byte copy of what was there before")
	}
	if got := cellValue(t, backups[0], "Расходы", "I3"); got != "" {
		t.Error("the backup must predate the write")
	}
}

// Ten is enough to undo a bad afternoon and few enough that the directory stays
// readable. Without a cap this grows without bound next to the file it protects.
func TestAssignIDs_keepsTheLastTenBackups(t *testing.T) {
	path := workbookWithExtraColumn(t)
	base := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	for i := range 13 {
		clock := func() time.Time { return base.Add(time.Duration(i) * time.Minute) }
		if err := financexlsx.AssignIDs(path, map[string]string{"expense-r3": "01A"}, clock); err != nil {
			t.Fatalf("AssignIDs run %d: %v", i+1, err)
		}
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".backup", "*.xlsx"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(backups) != 10 {
		t.Errorf("got %d backups, want the last 10", len(backups))
	}
}

// An id for a row that is not there means the two sides disagree about the
// shape of the sheet. Writing the rest anyway would leave a half-identified
// ledger, which is the state hardest to recover from.
func TestAssignIDs_refusesAnUnknownRow(t *testing.T) {
	path := workbookWithExtraColumn(t)
	err := financexlsx.AssignIDs(path, map[string]string{"expense-r999": "01A"}, writeClock)
	if err == nil {
		t.Fatal("expected an error for a row that does not exist")
	}
	if got := cellValue(t, path, "Расходы", "I3"); got != "" {
		t.Error("a refused write must leave the workbook untouched")
	}
}

// Once the workbook carries ids, they are the identity. The positional
// fallback is only for a sheet that has never been synced.
func TestRead_prefersTheStoredID(t *testing.T) {
	path := workbookWithExtraColumn(t)
	if err := financexlsx.AssignIDs(path, map[string]string{
		"expense-r3": "01A", "expense-r4": "01B", "income-r3": "01C",
	}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}

	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := map[string]bool{}
	for _, tx := range led.Transactions {
		got[tx.ID()] = true
	}
	for _, want := range []string{"01A", "01B", "01C"} {
		if !got[want] {
			t.Errorf("stored id %q was not used; ids read = %v", want, got)
		}
	}
}

// The workbook is personal and is kept at 0600. Saving must not widen that:
// excelize creates its own file, and a ledger that quietly becomes
// world-readable — or executable — is a worse outcome than a failed write.
func TestAssignIDs_preservesFileMode(t *testing.T) {
	path := workbookWithExtraColumn(t)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := financexlsx.AssignIDs(path, map[string]string{"expense-r3": "01A"}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("mode = %04o, want %04o unchanged", after.Mode().Perm(), before.Mode().Perm())
	}
}
