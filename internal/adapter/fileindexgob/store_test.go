package fileindexgob_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/fileindexgob"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

func TestStore_RoundTrip(t *testing.T) {
	st := fileindexgob.Store{Path: filepath.Join(t.TempDir(), "sub", "index.gob")}
	if _, ok, err := st.Load(); err != nil || ok {
		t.Fatalf("пустой Load = %v, %v", ok, err)
	}
	in := filesearch.Snapshot{
		Version:  filesearch.SnapshotVersion,
		Docs:     []filesearch.DocRecord{{Path: "/w/a.md", Size: 3, ModUnixNano: 42}},
		Terms:    []string{"договор"},
		Postings: [][]filesearch.Posting{{{Doc: 0, TF: 2}}},
	}
	if err := st.Save(in); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	// В индексе — пути и слова из личных файлов: читать его только владельцу.
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права %v, want 0600", info.Mode().Perm())
	}
	out, ok, err := st.Load()
	if err != nil || !ok || out.Docs[0].Path != "/w/a.md" || out.Postings[0][0].TF != 2 {
		t.Fatalf("Load = %+v, %v, %v", out, ok, err)
	}
}

func TestStore_CorruptOrOldFileIsTreatedAsMissing(t *testing.T) {
	// Испорченный или старый снимок — не авария: индекс просто соберется
	// заново целиком. Падать из-за кэша значило бы не открыть окно вовсе.
	dir := t.TempDir()
	st := fileindexgob.Store{Path: filepath.Join(dir, "index.gob")}
	if err := os.WriteFile(st.Path, []byte("not gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Load(); ok || err != nil {
		t.Fatalf("битый снимок: ok=%v err=%v", ok, err)
	}
	if err := st.Save(filesearch.Snapshot{Version: filesearch.SnapshotVersion + 7}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.Load(); ok || err != nil {
		t.Fatalf("чужая версия: ok=%v err=%v", ok, err)
	}
}
