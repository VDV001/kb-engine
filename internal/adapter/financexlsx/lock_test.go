package financexlsx_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// CheckLock knew one editor. The owner opens this workbook in whatever is at
// hand, and the two families of editors mark an open file differently:
//
//	LibreOffice   .~lock.Учёт_финансов.xlsx#
//	Excel         ~$Учёт_финансов.xlsx
//
// Writing underneath either one produces two divergent versions, and the one
// the editor holds wins the moment it saves — taking the rows written meanwhile
// with it. Recognising only the first is how a write gets promised, performed,
// and then quietly undone by the editor.
func TestCheckLock_recognisesEveryEditorsLockFile(t *testing.T) {
	tests := map[string]func(dir, base string) string{
		"LibreOffice": func(dir, base string) string {
			return filepath.Join(dir, ".~lock."+base+"#")
		},
		"Excel": func(dir, base string) string {
			return filepath.Join(dir, "~$"+base)
		},
	}

	for name, lockName := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Учёт_финансов.xlsx")
			if err := os.WriteFile(path, []byte("workbook"), 0o600); err != nil {
				t.Fatalf("create workbook: %v", err)
			}

			if err := financexlsx.CheckLock(path); err != nil {
				t.Fatalf("CheckLock on an unlocked workbook = %v, want nil", err)
			}

			lock := lockName(dir, filepath.Base(path))
			if err := os.WriteFile(lock, []byte("owner"), 0o600); err != nil {
				t.Fatalf("create lock: %v", err)
			}

			err := financexlsx.CheckLock(path)
			if !errors.Is(err, financexlsx.ErrWorkbookLocked) {
				t.Errorf("CheckLock with %s = %v, want ErrWorkbookLocked", filepath.Base(lock), err)
			}
		})
	}
}

// A lock check that cannot look is not a lock check that found nothing. Any
// answer other than "the file is not there" has to fail closed: a directory the
// process cannot stat, an I/O error on the volume holding it. Reading those as
// "unlocked" is the same silent outcome as not checking at all.
func TestCheckLock_failsClosedWhenItCannotLook(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked-away")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sub, "Учёт_финансов.xlsx")
	if err := os.WriteFile(path, []byte("workbook"), 0o600); err != nil {
		t.Fatalf("create workbook: %v", err)
	}
	// No execute bit: the file inside can no longer be stat'ed by name.
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so there is nothing to observe here")
	}

	err := financexlsx.CheckLock(path)
	if err == nil {
		t.Fatal("CheckLock on an unreadable directory = nil, want an error")
	}
	if errors.Is(err, financexlsx.ErrWorkbookLocked) {
		t.Errorf("want the stat failure reported as itself, got ErrWorkbookLocked: %v", err)
	}
}
