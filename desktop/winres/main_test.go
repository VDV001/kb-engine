package main

import (
	"bytes"
	"debug/pe"
	"os"
	"path/filepath"
	"testing"
)

// Объект ресурсов читается стандартным debug/pe: одна секция .rsrc, по
// перемещению на каждую запись данных, а группа значков стоит под №3 — его
// грузят Wails и трей. Связка с компоновщиком проверена сборкой kb.exe (PR #637).
func TestCOFFObject(t *testing.T) {
	dir := t.TempDir()
	syso, ico := filepath.Join(dir, "r.syso"), filepath.Join(dir, "dvk.ico")
	if err := run("../build/windows", syso, ico); err != nil {
		t.Fatal(err)
	}
	f, err := pe.Open(syso)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	s := f.Section(".rsrc")
	if s == nil || len(f.Sections) != 1 || f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Fatalf("секции %v, машина %#x", f.Sections, f.Machine)
	}
	want := len(iconSizes) + 2 // значки, группа, манифест
	if len(s.Relocs) != want {
		t.Fatalf("перемещений %d, ждали %d", len(s.Relocs), want)
	}
	for _, r := range s.Relocs {
		if r.Type != 3 || r.SymbolTableIndex != 0 {
			t.Fatalf("перемещение %+v: ждали ADDR32NB к символу секции", r)
		}
	}
	data, _ := s.Data()
	if !bytes.Contains(data, []byte("permonitorv2")) {
		t.Fatal("манифест без PerMonitorV2")
	}
	raw, _ := os.ReadFile(ico)
	if len(raw) < 6 || raw[2] != 1 || int(raw[4]) != len(iconSizes) {
		t.Fatalf(".ico: заголовок % x", raw[:6])
	}
}

// Группа значков: №3 среди ресурсов, 256 записан нулем.
func TestGroupIcon(t *testing.T) {
	g := groupIcon([][]byte{{1}, {2}, {3}, {4}, {5}, {6}})
	if len(g) != 6+14*6 || g[6+14*5] != 0 || g[6] != 16 {
		t.Fatalf("группа: % x", g)
	}
}
