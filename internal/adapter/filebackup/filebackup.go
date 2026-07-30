// Package filebackup keeps a rotating copy of a file next to it, so a write
// that turns out to be wrong is recoverable.
//
// It exists because two adapters need the same guarantee and the guarantee is
// easy to get subtly wrong: the copy has to be taken before the first mutation,
// the names have to sort chronologically, and pruning one kind of file must not
// delete another kind sitting in the same directory.
//
// The mechanism is deliberately dumb — a copy, not a diff and not a history.
// The files it protects are hand-kept records whose only other version is
// whatever the owner remembers.
package filebackup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// DirName lives next to the protected file, hidden, so backups travel with the
// file they protect.
const DirName = ".backup"

// Snapshot copies path into DirName next to it, then trims the directory to the
// newest keep files of the same extension.
//
// Only the same extension is pruned: a ledger and a workbook can share a
// directory, and rotating one must never delete the other's history.
//
// A missing source is not an error. Nothing has been written yet, so there is
// nothing to protect, and refusing the first write of a new file would be an
// odd way to keep it safe.
func Snapshot(path string, now func() time.Time, keep int) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", filepath.Base(path), err)
	}

	dir := filepath.Join(filepath.Dir(path), DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	stamp := now().UTC().Format("2006-01-02T15-04-05Z")
	dst := filepath.Join(dir, fmt.Sprintf("%s.%s%s", base, stamp, ext))

	if err := copyFile(path, dst); err != nil {
		return err
	}
	return prune(dir, ext, keep)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s for backup: %w", filepath.Base(src), err)
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

// prune keeps the newest keep files carrying ext. Names carry a sortable
// timestamp, so lexical order is chronological order.
func prune(dir, ext string, keep int) error {
	found, err := filepath.Glob(filepath.Join(dir, "*"+ext))
	if err != nil {
		return fmt.Errorf("list backups: %w", err)
	}
	if len(found) <= keep {
		return nil
	}
	slices.Sort(found)
	for _, old := range found[:len(found)-keep] {
		if err := os.Remove(old); err != nil {
			return fmt.Errorf("prune backup: %w", err)
		}
	}
	return nil
}
