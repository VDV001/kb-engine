package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type zf struct {
	name string
	body string
	mode os.FileMode
}

func makeZip(t *testing.T, files ...zf) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		h.SetMode(f.mode)
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(f.body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "DVK.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Распаковка не выходит из своей папки и не берет ссылки; исполняемый бит
// сохраняется (бандл без него не запустится) (#651).
func TestExtractZipIsStrict(t *testing.T) {
	ok := makeZip(t, zf{"kb.app/Contents/MacOS/kb", "bin", 0o755}, zf{"kb.app/Contents/Info.plist", "plist", 0o644})
	dir := t.TempDir()
	if err := extractZip(ok, dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "kb.app/Contents/MacOS/kb"))
	if err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("exec bit lost: %v %v", st, err)
	}
	for name, z := range map[string]string{
		"zip-slip": makeZip(t, zf{"../evil", "x", 0o644}),
		"absolute": makeZip(t, zf{"/etc/evil", "x", 0o644}),
		"symlink":  makeZip(t, zf{"kb.app/link", "/etc/passwd", os.ModeSymlink | 0o777}),
		"too big":  makeZip(t, zf{"kb.app/big", string(make([]byte, 2<<20)), 0o644}),
	} {
		d := t.TempDir()
		if err := extractZip(z, d, 1<<20); err == nil {
			t.Errorf("%s accepted", name)
		}
		if left, _ := os.ReadDir(d); name != "too big" && len(left) != 0 {
			t.Errorf("%s left files: %d", name, len(left))
		}
	}
}

// Подмена бандла: новый на месте, прежний — рядом .old; негодный архив
// оставляет окно как было и не оставляет мусора (#651).
func TestInstallMac(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "kb.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents/MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents/MacOS/kb"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := makeZip(t, zf{"kb.app/Contents/Info.plist", "no binary", 0o644})
	if err := installMac(bad, app); err == nil {
		t.Fatal("a bundle without the binary accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents/MacOS/kb")); string(b) != "old" {
		t.Errorf("window changed by a bad archive: %q", b)
	}
	if left, _ := os.ReadDir(root); len(left) != 1 {
		t.Errorf("litter after a bad archive: %v", left)
	}
	good := makeZip(t, zf{"kb.app/Contents/MacOS/kb", "new", 0o755})
	if err := installMac(good, app); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents/MacOS/kb")); string(b) != "new" {
		t.Errorf("not replaced: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(app+".old", "Contents/MacOS/kb")); string(b) != "old" {
		t.Errorf("no backup: %q", b)
	}
	if err := installMac(good, app); err != nil { // повторная установка заменяет прежний .old
		t.Errorf("second install: %v", err)
	}
}

// Путь бандла берется из пути исполняемого файла: не бандл — нечего менять.
func TestBundleOf(t *testing.T) {
	if got, ok := bundleOf("/Applications/kb.app/Contents/MacOS/kb"); !ok || got != "/Applications/kb.app" {
		t.Errorf("%q %v", got, ok)
	}
	if _, ok := bundleOf("/usr/local/bin/kb"); ok {
		t.Error("a bare binary is not a bundle")
	}
}
