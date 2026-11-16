// Package qrcode draws a QR code (ISO/IEC 18004) without dependencies: байтовый
// режим, коррекция M, версии 1–20 (до 666 байт). Нужен окну на Windows, где нет
// CoreImage (#622, этап 5); на Маке окно рисует QR системой, а этот кодировщик
// проверяется там детектором Apple (desktop/qr_darwin_test.go) — проверка не
// закольцована на сам кодировщик.
//
// Порядок шагов и формулы — по стандарту в изложении Project Nayuki.
package qrcode

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// ErrTooLong — текст не помещается в версию 20 с коррекцией M.
var ErrTooLong = errors.New("текст длиннее 666 байт — в QR версии 20 с коррекцией M не помещается")

// ecM — коррекция M по версиям 1–20: байт коррекции на блок, затем группы
// блоков {число блоков, байт данных в блоке}.
var ecM = [21]struct {
	ec     int
	groups [2][2]int
}{
	{},
	{10, [2][2]int{{1, 16}}}, {16, [2][2]int{{1, 28}}}, {26, [2][2]int{{1, 44}}}, {18, [2][2]int{{2, 32}}},
	{24, [2][2]int{{2, 43}}}, {16, [2][2]int{{4, 27}}}, {18, [2][2]int{{4, 31}}}, {22, [2][2]int{{2, 38}, {2, 39}}},
	{22, [2][2]int{{3, 36}, {2, 37}}}, {26, [2][2]int{{4, 43}, {1, 44}}}, {30, [2][2]int{{1, 50}, {4, 51}}},
	{22, [2][2]int{{6, 36}, {2, 37}}}, {22, [2][2]int{{8, 37}, {1, 38}}}, {24, [2][2]int{{4, 40}, {5, 41}}},
	{24, [2][2]int{{5, 41}, {5, 42}}}, {28, [2][2]int{{7, 45}, {3, 46}}}, {28, [2][2]int{{10, 46}, {1, 47}}},
	{26, [2][2]int{{9, 43}, {4, 44}}}, {26, [2][2]int{{3, 44}, {11, 45}}}, {26, [2][2]int{{3, 41}, {13, 42}}},
}

const maxVersion = 20

func dataCodewords(ver int) int {
	n := 0
	for _, g := range ecM[ver].groups {
		n += g[0] * g[1]
	}
	return n
}

// rawModules — модули под данные и коррекцию: все, кроме служебных узоров.
func rawModules(ver int) int {
	r := (16*ver+128)*ver + 64
	if ver >= 2 {
		n := ver/7 + 2
		r -= (25*n-10)*n - 55
		if ver >= 7 {
			r -= 36
		}
	}
	return r
}

// Code is a drawn QR code: Size×Size модулей, true — темный.
type Code struct {
	Size    int
	modules [][]bool
	fn      [][]bool // служебные модули: маска их не трогает
}

// Dark reports whether module (x, y) is dark.
func (c *Code) Dark(x, y int) bool { return c.modules[y][x] }

// Encode builds the smallest QR code for text with error correction M.
func Encode(text []byte) (*Code, error) {
	ver := 1
	for ; ver <= maxVersion; ver++ {
		lenBits := 8
		if ver >= 10 {
			lenBits = 16
		}
		if 4+lenBits+8*len(text) <= 8*dataCodewords(ver) {
			break
		}
	}
	if ver > maxVersion {
		return nil, ErrTooLong
	}
	data := encodeData(text, ver)
	all := addECAndInterleave(data, ver)

	c := &Code{Size: 4*ver + 17}
	c.modules, c.fn = grid(c.Size), grid(c.Size)
	c.drawFunctionPatterns(ver)
	c.drawCodewords(all)
	best, bestPenalty := 0, -1
	for m := range 8 {
		c.applyMask(m)
		c.drawFormat(m)
		if p := c.penalty(); bestPenalty < 0 || p < bestPenalty {
			best, bestPenalty = m, p
		}
		c.applyMask(m) // маска — XOR: второй раз снимает ее
	}
	c.applyMask(best)
	c.drawFormat(best)
	return c, nil
}

func grid(n int) [][]bool {
	g := make([][]bool, n)
	for i := range g {
		g[i] = make([]bool, n)
	}
	return g
}

// --- данные -----------------------------------------------------------------

type bitBuf []bool

func (b *bitBuf) put(v uint, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, v>>uint(i)&1 == 1)
	}
}

func encodeData(text []byte, ver int) []byte {
	var b bitBuf
	b.put(0b0100, 4) // байтовый режим
	lenBits := 8
	if ver >= 10 {
		lenBits = 16
	}
	b.put(uint(len(text)), lenBits)
	for _, c := range text {
		b.put(uint(c), 8)
	}
	capBits := 8 * dataCodewords(ver)
	b.put(0, min(4, capBits-len(b))) // терминатор
	b.put(0, (8-len(b)%8)%8)
	out := make([]byte, 0, capBits/8)
	for i := 0; i < len(b); i += 8 {
		var v byte
		for j := range 8 {
			if b[i+j] {
				v |= 1 << uint(7-j)
			}
		}
		out = append(out, v)
	}
	for pad := byte(0xEC); len(out) < capBits/8; pad ^= 0xEC ^ 0x11 {
		out = append(out, pad)
	}
	return out
}

func addECAndInterleave(data []byte, ver int) []byte {
	e := ecM[ver]
	gen := rsGenerator(e.ec)
	var blocks, ecs [][]byte
	k := 0
	for _, g := range e.groups {
		for range g[0] {
			blk := data[k : k+g[1]]
			k += g[1]
			blocks = append(blocks, blk)
			ecs = append(ecs, rsRemainder(blk, gen))
		}
	}
	var out []byte
	for i := 0; ; i++ {
		any := false
		for _, blk := range blocks {
			if i < len(blk) {
				out = append(out, blk[i])
				any = true
			}
		}
		if !any {
			break
		}
	}
	for i := range e.ec {
		for _, ec := range ecs {
			out = append(out, ec[i])
		}
	}
	return out
}

// --- Рид — Соломон над GF(256), многочлен 0x11D ------------------------------

func gfMul(x, y byte) byte {
	var z byte
	for i := 7; i >= 0; i-- {
		hi := z >> 7
		z = z<<1 ^ hi*0x1D
		if y>>uint(i)&1 == 1 {
			z ^= x
		}
	}
	return z
}

// rsGenerator — коэффициенты (x−α⁰)…(x−α^(n−1)) без старшего, от высшей степени.
func rsGenerator(n int) []byte {
	g := make([]byte, n)
	g[n-1] = 1
	root := byte(1)
	for range n {
		for j := range g {
			g[j] = gfMul(g[j], root)
			if j+1 < len(g) {
				g[j] ^= g[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return g
}

func rsRemainder(data, gen []byte) []byte {
	r := make([]byte, len(gen))
	for _, b := range data {
		f := b ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i := range r {
			r[i] ^= gfMul(gen[i], f)
		}
	}
	return r
}

// --- узоры ------------------------------------------------------------------

func (c *Code) set(x, y int, dark bool) {
	c.modules[y][x] = dark
	c.fn[y][x] = true
}

func (c *Code) drawFunctionPatterns(ver int) {
	for i := range c.Size { // полосы синхронизации
		c.set(6, i, i%2 == 0)
		c.set(i, 6, i%2 == 0)
	}
	c.finder(3, 3)
	c.finder(c.Size-4, 3)
	c.finder(3, c.Size-4)
	c.drawAlignment(ver)
	c.drawFormat(0) // занять место; настоящий формат — после выбора маски
	c.drawVersion(ver)
}

func (c *Code) drawAlignment(ver int) {
	pos := alignmentPositions(ver)
	last := len(pos) - 1
	for i, x := range pos {
		for j, y := range pos {
			if i == 0 && j == 0 || i == 0 && j == last || i == last && j == 0 {
				continue // углы с поисковыми узорами
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					c.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
}

// drawVersion — номер версии с BCH(18,6), с версии 7, двумя копиями.
func (c *Code) drawVersion(ver int) {
	if ver < 7 {
		return
	}
	rem := ver
	for range 12 {
		rem = rem<<1 ^ (rem>>11)*0x1F25
	}
	bits := ver<<12 | rem
	for i := range 18 {
		dark := bits>>uint(i)&1 == 1
		a, b := c.Size-11+i%3, i/3
		c.set(a, b, dark)
		c.set(b, a, dark)
	}
}

func (c *Code) finder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
				continue
			}
			d := max(abs(dx), abs(dy))
			c.set(x, y, d != 2 && d != 4)
		}
	}
}

func alignmentPositions(ver int) []int {
	if ver == 1 {
		return nil
	}
	n := ver/7 + 2
	step := (ver*8 + n*3 + 5) / (n*4 - 4) * 2
	pos := make([]int, n)
	pos[0] = 6
	for i, p := n-1, 4*ver+10; i >= 1; i, p = i-1, p-step {
		pos[i] = p
	}
	return pos
}

func (c *Code) drawFormat(mask int) {
	data := 0<<3 | mask // коррекция M = 00
	rem := data
	for range 10 {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>uint(i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		c.set(8, i, bit(i))
	}
	c.set(8, 7, bit(6))
	c.set(8, 8, bit(7))
	c.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.set(14-i, 8, bit(i))
	}
	for i := range 8 {
		c.set(c.Size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.set(8, c.Size-15+i, bit(i))
	}
	c.set(8, c.Size-8, true) // темный модуль
}

// --- размещение и маска -------------------------------------------------------

func (c *Code) drawCodewords(data []byte) {
	i := 0
	for right := c.Size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := range c.Size {
			for j := range 2 {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = c.Size - 1 - vert
				}
				if c.fn[y][x] {
					continue
				}
				if i < len(data)*8 {
					c.modules[y][x] = data[i>>3]>>uint(7-i&7)&1 == 1
					i++
				}
			}
		}
	}
}

// masks — восемь масок стандарта: true — модуль инвертируется.
var masks = [8]func(x, y int) bool{
	func(x, y int) bool { return (x+y)%2 == 0 },
	func(_, y int) bool { return y%2 == 0 },
	func(x, _ int) bool { return x%3 == 0 },
	func(x, y int) bool { return (x+y)%3 == 0 },
	func(x, y int) bool { return (x/3+y/2)%2 == 0 },
	func(x, y int) bool { return x*y%2+x*y%3 == 0 },
	func(x, y int) bool { return (x*y%2+x*y%3)%2 == 0 },
	func(x, y int) bool { return ((x+y)%2+x*y%3)%2 == 0 },
}

func (c *Code) applyMask(m int) {
	inv := masks[m]
	for y := range c.Size {
		for x := range c.Size {
			if !c.fn[y][x] && inv(x, y) {
				c.modules[y][x] = !c.modules[y][x]
			}
		}
	}
}

// penalty — штраф маски по четырем правилам стандарта: меньше — читается лучше.
// На саму читаемость не влияет: любая маска верна, штраф выбирает удобную.
func (c *Code) penalty() int {
	p := c.penaltyBalance()
	for _, col := range []bool{false, true} {
		for i := range c.Size {
			line := c.line(i, col)
			p += penaltyRuns(line) + penaltyFinderLike(line)
		}
	}
	return p + c.penaltyBlocks()
}

// line — строка i или столбец i.
func (c *Code) line(i int, col bool) []bool {
	out := make([]bool, c.Size)
	for k := range c.Size {
		if col {
			out[k] = c.modules[k][i]
		} else {
			out[k] = c.modules[i][k]
		}
	}
	return out
}

// penaltyRuns: 5 и больше одинаковых подряд — 3 плюс по одному за каждый сверх 5.
func penaltyRuns(line []bool) int {
	p, run := 0, 1
	for k := 1; k <= len(line); k++ {
		if k < len(line) && line[k] == line[k-1] {
			run++
			continue
		}
		if run >= 5 {
			p += run - 2
		}
		run = 1
	}
	return p
}

var (
	finderThenLight = [11]bool{true, false, true, true, true, false, true}
	lightThenFinder = [11]bool{4: true, 6: true, 7: true, 8: true, 10: true}
)

// penaltyFinderLike: узор 1:1:3:1:1 со светлыми четырьмя с одной стороны — 40.
func penaltyFinderLike(line []bool) int {
	p := 0
	for k := 0; k+11 <= len(line); k++ {
		w := [11]bool(line[k : k+11])
		if w == finderThenLight || w == lightThenFinder {
			p += 40
		}
	}
	return p
}

// penaltyBlocks: каждый квадрат 2×2 одного цвета — 3.
func (c *Code) penaltyBlocks() int {
	p := 0
	for y := 0; y+1 < c.Size; y++ {
		for x := 0; x+1 < c.Size; x++ {
			v := c.modules[y][x]
			if v == c.modules[y][x+1] && v == c.modules[y+1][x] && v == c.modules[y+1][x+1] {
				p += 3
			}
		}
	}
	return p
}

// penaltyBalance: по 10 за каждые 5 % отклонения доли темных от половины.
func (c *Code) penaltyBalance() int {
	dark, total := 0, c.Size*c.Size
	for _, row := range c.modules {
		for _, v := range row {
			if v {
				dark++
			}
		}
	}
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return max(k, 0) * 10
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// PNG renders the code: scale пикселей на модуль, тихая зона в 4 модуля.
func (c *Code) PNG(scale int) ([]byte, error) {
	const quiet = 4
	side := (c.Size + 2*quiet) * scale
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y := range c.Size {
		for x := range c.Size {
			if !c.modules[y][x] {
				continue
			}
			for dy := range scale {
				for dx := range scale {
					img.SetGray((x+quiet)*scale+dx, (y+quiet)*scale+dy, color.Gray{})
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
