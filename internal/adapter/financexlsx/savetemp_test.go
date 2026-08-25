package financexlsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// swapSaveWorkbook installs a stand-in write step for one test.
func swapSaveWorkbook(t *testing.T, fn func(*excelize.File, string) error) {
	t.Helper()
	prev := saveWorkbook
	saveWorkbook = fn
	t.Cleanup(func() { saveWorkbook = prev })
}

// workbookAt puts a small workbook on disk at mode and returns its path.
func workbookAt(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.xlsx")
	f := excelize.NewFile()
	t.Cleanup(func() { _ = f.Close() })
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("prepare workbook: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return path
}

// The workbook is personal. AssignIDs_preservesFileMode already guards the
// final file, but the copy we write first is a full second copy of the ledger
// sitting next to it, and excelize creates it with its own mode. The narrow
// mode has to be in place before the bytes are, not after.
func TestSaveAtomically_tempFileIsNotWiderThanTheWorkbook(t *testing.T) {
	path := workbookAt(t, 0o600)

	var seen os.FileMode
	var sawFile bool
	swapSaveWorkbook(t, func(f *excelize.File, tmp string) error {
		if st, err := os.Stat(tmp); err == nil {
			sawFile, seen = true, st.Mode().Perm()
		}
		return f.SaveAs(tmp)
	})

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := saveAtomically(f, path); err != nil {
		t.Fatalf("saveAtomically: %v", err)
	}

	if !sawFile {
		t.Fatal("temp file did not exist when the write step ran; the workbook's mode can never be applied to it in time")
	}
	if seen != 0o600 {
		t.Errorf("temp file mode during the write = %04o, want %04o — a copy of the ledger was readable by everyone", seen, 0o600)
	}
}

// A failed write must not leave a copy of the ledger lying around: Chmod and
// Rename both clean up after themselves, and the write step is the one that
// does not.
func TestSaveAtomically_removesTempFileWhenWriteFails(t *testing.T) {
	path := workbookAt(t, 0o600)
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+filepath.Base(path))

	wantErr := errors.New("no space left on device")
	swapSaveWorkbook(t, func(f *excelize.File, tmp string) error {
		// The real SaveAs creates the file and only then fails part-way, so the
		// stand-in has to leave one behind too.
		if err := os.WriteFile(tmp, []byte("half a workbook"), 0o600); err != nil {
			return err
		}
		return wantErr
	})

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	err := saveAtomically(f, path)
	if !errors.Is(err, wantErr) {
		t.Fatalf("saveAtomically error = %v, want it to carry %v", err, wantErr)
	}

	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("temp file %s still exists after a failed write", filepath.Base(tmp))
	}
}

// A temp file left over from an earlier failed write is exactly the case this
// guard exists for, and it is the one O_CREATE cannot fix: an existing file
// keeps its own mode. Observed on a full volume — a .tmp- copy stayed behind
// at rwxr-xr-x, and the next write would have reused it.
func TestSaveAtomically_narrowsAStaleTempFile(t *testing.T) {
	path := workbookAt(t, 0o600)
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	if err := os.WriteFile(tmp, []byte("left over from a failed write"), 0o755); err != nil {
		t.Fatalf("prepare stale temp file: %v", err)
	}

	var seen os.FileMode
	swapSaveWorkbook(t, func(f *excelize.File, tmp string) error {
		st, err := os.Stat(tmp)
		if err != nil {
			return err
		}
		seen = st.Mode().Perm()
		return f.SaveAs(tmp)
	})

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := saveAtomically(f, path); err != nil {
		t.Fatalf("saveAtomically: %v", err)
	}

	if seen != 0o600 {
		t.Errorf("stale temp file kept mode %04o during the write, want %04o", seen, 0o600)
	}
}
