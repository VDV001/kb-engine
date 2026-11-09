package memorydir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// На exFAT, SMB и части облачных папок жестких ссылок нет: перенос без перезаписи
// обязан работать и без них (ревью #521).
func TestMoveByCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")
	if err := os.WriteFile(src, []byte("тело"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := moveByCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "тело" {
		t.Errorf("dst = %q", b)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Error("источник остался")
	}
	if info, _ := os.Stat(dst); info.Mode().Perm() != 0o600 {
		t.Errorf("права %v", info.Mode().Perm())
	}

	_ = os.WriteFile(src, []byte("другое"), 0o600)
	if err := moveByCopy(src, dst); !errors.Is(err, domain.ErrNoteExists) {
		t.Fatalf("занятое имя: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "тело" {
		t.Errorf("существующий файл затерт: %q", b)
	}
	if _, err := os.Stat(src); err != nil {
		t.Error("при отказе источник должен остаться")
	}
}
