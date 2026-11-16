// winres собирает ресурсы окна DVK для Windows (#622, этап 5): значок exe,
// окна и трея (группа №3 — ее грузит Wails, winc.AppIconID) и манифест с
// PerMonitorV2 DPI — без него окно на экране 125–150 % размыто. Пишет
// COFF-объект rsrc_windows_amd64.syso, который go build вшивает сам, и
// dvk.ico для ярлыков установщика. Без зависимостей и внешних программ.
//
//	go run ./winres -dir build/windows -syso rsrc_windows_amd64.syso -ico build/windows/dvk.ico
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
)

// Типы ресурсов Windows.
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtManifest  = 24
	appIconID   = 3      // winc.AppIconID в Wails v2
	langEnUS    = 0x0409 // язык записи ресурса; Windows берет и при другом языке системы
)

// errNotSquare — картинка значка не того размера или не PNG.
var errNotSquare = errors.New("нужен квадратный PNG своего размера")

// iconSizes — размеры значка, файлы build/windows/icon-N.png.
var iconSizes = []int{16, 24, 32, 48, 64, 256}

type resource struct {
	typ, id uint32
	data    []byte
}

func main() {
	dir := flag.String("dir", "build/windows", "папка с icon-N.png и kb.exe.manifest")
	syso := flag.String("syso", "rsrc_windows_amd64.syso", "куда писать объект ресурсов")
	ico := flag.String("ico", "build/windows/dvk.ico", "куда писать .ico")
	flag.Parse()
	if err := run(*dir, *syso, *ico); err != nil {
		fmt.Fprintln(os.Stderr, "winres:", err)
		os.Exit(1)
	}
}

func run(dir, sysoPath, icoPath string) error {
	pngs := make([][]byte, len(iconSizes))
	for i, n := range iconSizes {
		b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("icon-%d.png", n)))
		if err != nil {
			return err
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width != n || cfg.Height != n {
			return fmt.Errorf("icon-%d.png: не PNG %d×%d: %w", n, n, n, errors.Join(err, errNotSquare))
		}
		pngs[i] = b
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "kb.exe.manifest"))
	if err != nil {
		return err
	}
	res := []resource{{typ: rtGroupIcon, id: appIconID, data: groupIcon(pngs)}, {typ: rtManifest, id: 1, data: manifest}}
	for i, p := range pngs {
		res = append(res, resource{typ: rtIcon, id: uint32(i + 1), data: p})
	}
	if err := os.WriteFile(sysoPath, coff(res), 0o644); err != nil {
		return err
	}
	return os.WriteFile(icoPath, icoFile(pngs), 0o644)
}

// iconEntry — общие поля записи значка в .ico и в группе ресурса.
func iconEntry(b *bytes.Buffer, size int, n int) {
	w := byte(size)
	if size >= 256 {
		w = 0 // 0 значит 256
	}
	b.Write([]byte{w, w, 0, 0})
	le(b, uint16(1), uint16(32), uint32(n))
}

// groupIcon — GRPICONDIR: номера RT_ICON вместо смещений.
func groupIcon(pngs [][]byte) []byte {
	var b bytes.Buffer
	le(&b, uint16(0), uint16(1), uint16(len(pngs)))
	for i, p := range pngs {
		iconEntry(&b, iconSizes[i], len(p))
		le(&b, uint16(i+1))
	}
	return b.Bytes()
}

// icoFile — файл .ico с PNG внутри (Windows Vista и новее).
func icoFile(pngs [][]byte) []byte {
	var b bytes.Buffer
	le(&b, uint16(0), uint16(1), uint16(len(pngs)))
	off := 6 + 16*len(pngs)
	for i, p := range pngs {
		iconEntry(&b, iconSizes[i], len(p))
		le(&b, uint32(off))
		off += len(p)
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}

func le(b *bytes.Buffer, vs ...any) {
	for _, v := range vs {
		_ = binary.Write(b, binary.LittleEndian, v)
	}
}

// coff builds an AMD64 COFF object with one .rsrc section: дерево ресурсов
// тип → номер → язык, записи данных и сами данные. Адрес данных в записи —
// RVA, его ставит компоновщик по перемещению IMAGE_REL_AMD64_ADDR32NB
// относительно символа секции; в поле лежит смещение внутри секции.
func coff(res []resource) []byte {
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []uint32
	byType := map[uint32][]int{}
	for i, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}
	const dirSize, entrySize, dataEntrySize = 16, 8, 16
	// Раскладка: корень, каталоги номеров, каталоги языков, записи данных, данные.
	off := dirSize + entrySize*len(types)
	nameDir := map[uint32]int{}
	for _, t := range types {
		nameDir[t] = off
		off += dirSize + entrySize*len(byType[t])
	}
	langDir := make([]int, len(res))
	for i := range res {
		langDir[i] = off
		off += dirSize + entrySize
	}
	dataEntry := make([]int, len(res))
	for i := range res {
		dataEntry[i] = off
		off += dataEntrySize
	}
	dataOff := make([]int, len(res))
	for i, r := range res {
		off = (off + 7) &^ 7
		dataOff[i] = off
		off += len(r.data)
	}
	sec := make([]byte, off)
	put := func(at int, vs ...uint32) {
		for k, v := range vs {
			binary.LittleEndian.PutUint32(sec[at+4*k:], v)
		}
	}
	dir := func(at, ids int) { binary.LittleEndian.PutUint16(sec[at+14:], uint16(ids)) }
	const subdir = 0x80000000
	dir(0, len(types))
	for k, t := range types {
		put(dirSize+entrySize*k, t, subdir|uint32(nameDir[t]))
		dir(nameDir[t], len(byType[t]))
		for j, i := range byType[t] {
			put(nameDir[t]+dirSize+entrySize*j, res[i].id, subdir|uint32(langDir[i]))
		}
	}
	var relocs []int
	for i, r := range res {
		dir(langDir[i], 1)
		put(langDir[i]+dirSize, langEnUS, uint32(dataEntry[i]))
		put(dataEntry[i], uint32(dataOff[i]), uint32(len(r.data)), 0, 0)
		relocs = append(relocs, dataEntry[i])
		copy(sec[dataOff[i]:], r.data)
	}
	return object(sec, relocs)
}

// object wraps the .rsrc section into a COFF file: заголовок, секция,
// перемещения к символу секции, сам символ и пустая таблица строк.
func object(sec []byte, relocs []int) []byte {
	const fileHdr, secHdr = 20, 40
	relocAt := fileHdr + secHdr + len(sec)
	symAt := relocAt + 10*len(relocs)
	var b bytes.Buffer
	le(&b, uint16(0x8664), uint16(1), uint32(0), uint32(symAt), uint32(1), uint16(0), uint16(0))
	b.WriteString(".rsrc\x00\x00\x00")
	le(&b, uint32(0), uint32(0), uint32(len(sec)), uint32(fileHdr+secHdr), uint32(relocAt), uint32(0),
		uint16(len(relocs)), uint16(0), uint32(0x40000040)) // INITIALIZED_DATA | MEM_READ
	b.Write(sec)
	for _, at := range relocs {
		le(&b, uint32(at), uint32(0), uint16(3)) // IMAGE_REL_AMD64_ADDR32NB к символу 0
	}
	b.WriteString(".rsrc\x00\x00\x00")
	le(&b, uint32(0), int16(1), uint16(0), uint8(3), uint8(0)) // секция 1, IMAGE_SYM_CLASS_STATIC
	le(&b, uint32(4))                                          // пустая таблица строк
	return b.Bytes()
}
