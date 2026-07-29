package financexlsx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// ErrWorkbookLocked is returned when the workbook is open in another
// application.
var ErrWorkbookLocked = errors.New("workbook is locked by another application")

const (
	// idHeader marks the identity column. It sits in the header row so the
	// column explains itself to whoever opens the file next.
	idHeader = "id"
	// headerRow is where the workbook keeps its column names; row 1 is a title.
	headerRow = 2
	// backupDirName lives next to the workbook, hidden, so backups travel with
	// the file they protect.
	backupDirName = ".backup"
	// backupsKept is enough to undo a bad afternoon and few enough that the
	// directory stays readable.
	backupsKept = 10
)

// AssignIDs writes a stable identifier next to each row, adding the id column
// when the workbook does not have one yet.
//
// assign maps the positional id handed out by Read ("expense-r42") to the id to
// store. Positional ids are exactly a sheet-and-row locator, which is what they
// are good for; this is the call that retires them.
//
// Nothing is written until every id has been resolved to a cell. A workbook
// where half the rows carry an identity is the hardest state to recover from,
// so the choice is all or nothing.
func AssignIDs(path string, assign map[string]string, now func() time.Time) error {
	if err := CheckLock(path); err != nil {
		return err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	writes, idCols, err := resolvePlacements(f, assign)
	if err != nil {
		return err
	}

	if err := backup(path, now); err != nil {
		return err
	}

	for sheet, col := range idCols {
		cell, err := excelize.CoordinatesToCellName(col, headerRow)
		if err != nil {
			return fmt.Errorf("%s header: %w", sheet, err)
		}
		if err := f.SetCellStr(sheet, cell, idHeader); err != nil {
			return fmt.Errorf("%s header: %w", sheet, err)
		}
	}
	for _, w := range writes {
		// SetCellStr, not SetCellValue: a ULID is a string, and letting the
		// spreadsheet guess would turn some of them into numbers or dates.
		if err := f.SetCellStr(w.sheet, w.cell, w.id); err != nil {
			return fmt.Errorf("%s!%s: %w", w.sheet, w.cell, err)
		}
	}

	return saveAtomically(f, path)
}

// placement is one resolved write: which cell in which sheet gets which id.
type placement struct {
	sheet, cell, id string
}

// resolvePlacements turns positional ids into cells, failing before anything is
// written if any of them cannot be placed. It also reports the id column chosen
// per sheet, so the header can be written alongside.
func resolvePlacements(f *excelize.File, assign map[string]string) ([]placement, map[string]int, error) {
	var writes []placement
	idCols := map[string]int{}

	for posID, newID := range assign {
		sheet, row, err := parsePositionalID(posID)
		if err != nil {
			return nil, nil, err
		}
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, nil, fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if row < firstDataRow || row > len(rows) {
			return nil, nil, fmt.Errorf("%s: row %d does not exist (sheet has %d rows)", sheet, row, len(rows))
		}
		col, ok := idCols[sheet]
		if !ok {
			col = chooseIDColumn(rows, reservedWidth(kindOf(sheet)))
			idCols[sheet] = col
		}
		cell, err := excelize.CoordinatesToCellName(col, row)
		if err != nil {
			return nil, nil, fmt.Errorf("%s row %d: %w", sheet, row, err)
		}
		writes = append(writes, placement{sheet: sheet, cell: cell, id: newID})
	}

	// Deterministic order: the same input produces the same file, which keeps a
	// diff of the workbook meaningful.
	slices.SortFunc(writes, func(a, b placement) int {
		if c := strings.Compare(a.sheet, b.sheet); c != 0 {
			return c
		}
		return strings.Compare(a.cell, b.cell)
	})
	return writes, idCols, nil
}

// CheckLock reports whether an editor is holding the workbook. LibreOffice
// leaves .~lock.<name># next to the file for as long as it is open, and writing
// underneath that produces two divergent versions — one of which disappears
// without warning when the editor saves.
//
// Exported so a caller that is only planning a write — a dry run, say — can ask
// the same question the write will ask, instead of promising an outcome the
// real run would refuse.
func CheckLock(path string) error {
	lock := filepath.Join(filepath.Dir(path), ".~lock."+filepath.Base(path)+"#")
	if _, err := os.Stat(lock); err == nil {
		return fmt.Errorf("%w: close it and try again (%s)", ErrWorkbookLocked, lock)
	}
	return nil
}

// backup copies the workbook into .backup/ before it is touched, then trims the
// directory to the most recent backupsKept files.
//
// The workbook is four years of hand-kept records with no version history
// behind it, so every write leaves a way back.
func backup(path string, now func() time.Time) error {
	dir := filepath.Join(filepath.Dir(path), backupDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stamp := now().UTC().Format("2006-01-02T15-04-05Z")
	dst := filepath.Join(dir, fmt.Sprintf("%s.%s.xlsx", base, stamp))

	if err := copyFile(path, dst); err != nil {
		return err
	}
	return pruneBackups(dir)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open workbook for backup: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("write backup: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync backup: %w", err)
	}
	return out.Close()
}

// pruneBackups keeps the newest backupsKept files. Names carry a sortable
// timestamp, so lexical order is chronological order.
func pruneBackups(dir string) error {
	found, err := filepath.Glob(filepath.Join(dir, "*.xlsx"))
	if err != nil {
		return fmt.Errorf("list backups: %w", err)
	}
	if len(found) <= backupsKept {
		return nil
	}
	slices.Sort(found)
	for _, old := range found[:len(found)-backupsKept] {
		if err := os.Remove(old); err != nil {
			return fmt.Errorf("prune backup: %w", err)
		}
	}
	return nil
}

// saveAtomically writes to a temp file in the same directory and renames over
// the original, so an interrupted save cannot leave a truncated workbook.
//
// The original's permissions are carried over. excelize creates its own file
// with its own mode, and a personal ledger that quietly becomes world-readable
// is a worse outcome than a write that fails.
func saveAtomically(f *excelize.File, path string) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	if err := f.SaveAs(tmp); err != nil {
		return fmt.Errorf("write workbook: %w", err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("set workbook permissions: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace workbook: %w", err)
	}
	return nil
}

// parsePositionalID turns "expense-r42" back into the sheet and row it points
// at.
func parsePositionalID(id string) (sheet string, row int, err error) {
	kind, rawRow, ok := strings.Cut(id, "-r")
	if !ok {
		return "", 0, fmt.Errorf("unrecognized row id %q", id)
	}
	switch kind {
	case domain.KindExpense:
		sheet = sheetExpenses
	case domain.KindIncome:
		sheet = sheetIncome
	default:
		return "", 0, fmt.Errorf("unrecognized row id %q", id)
	}
	if row, err = strconv.Atoi(rawRow); err != nil {
		return "", 0, fmt.Errorf("unrecognized row id %q", id)
	}
	return sheet, row, nil
}

// findIDColumn returns the 1-based column carrying the id header, or 0 when the
// sheet has none. Reading never guesses: a sheet without the header simply has
// no stored identity yet.
func findIDColumn(rows [][]string) int {
	if len(rows) < headerRow {
		return 0
	}
	for i, v := range rows[headerRow-1] {
		if strings.EqualFold(strings.TrimSpace(v), idHeader) {
			return i + 1
		}
	}
	return 0
}

// chooseIDColumn returns the column to write ids into: the existing one, or the
// first column past both the sheet's documented width and everything it
// actually holds.
//
// Both bounds are needed, and each was learned the hard way. Past the filled
// cells, because the live ledger keeps bank names in an unlabelled eighth
// column on 19 rows and appending after the seven named ones would overwrite
// them. Past the documented width, because Источник is the seventh column and
// is usually empty, which makes it look free — ids written there are invisible
// until something reads the sheet back and finds a ULID where the source of the
// record should be.
func chooseIDColumn(rows [][]string, documented int) int {
	if col := findIDColumn(rows); col != 0 {
		return col
	}
	maxCol := documented
	for _, row := range rows {
		for c, v := range slices.Backward(row) {
			if strings.TrimSpace(v) != "" {
				maxCol = max(maxCol, c+1)
				break
			}
		}
	}
	return maxCol + 1
}

// IsPositionalID reports whether an id is the sheet-and-row placeholder Read
// hands out for a sheet that has no id column yet. The format is this package's
// invention, so the test for it belongs here rather than at the call site.
func IsPositionalID(id string) bool {
	_, _, err := parsePositionalID(id)
	return err == nil
}
