//go:build windows

// kb-listen.exe [--lang ru-RU] [--pause 1.2] [--check] [--record out.wav] —
// диктовка окна DVK на Windows (#622, этап 5): тот же протокол, что у
// desktop/listen/main.swift на Маке (строки JSON в stdout, стоп — закрытый
// stdin), но только запись для сервера распознавания (--record): своего
// распознавания речи без сети в Windows для окна нет, текст дает облако или
// свой сервер (решение владельца 15.11). Микрофон — winmm waveIn, 16 кГц
// моно 16 бит, без cgo и зависимостей.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

const (
	bufBytes      = 3200 // 0,1 с звука
	numBufs       = 8
	noSpeechLimit = 10 * time.Second
	whdrDone      = 0x1
	waveMapper    = 0xFFFFFFFF
	hdrSize       = 48 // WAVEHDR на 64 битах
)

var (
	winmm         = syscall.NewLazyDLL("winmm.dll")
	waveInOpen    = winmm.NewProc("waveInOpen")
	waveInPrepare = winmm.NewProc("waveInPrepareHeader")
	waveInUnprep  = winmm.NewProc("waveInUnprepareHeader")
	waveInAdd     = winmm.NewProc("waveInAddBuffer")
	waveInStart   = winmm.NewProc("waveInStart")
	waveInReset   = winmm.NewProc("waveInReset")
	waveInClose   = winmm.NewProc("waveInClose")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	virtualAlloc  = kernel32.NewProc("VirtualAlloc")
	moveMemory    = kernel32.NewProc("RtlMoveMemory")
)

type waveFormat struct {
	tag              uint16
	channels         uint16
	samplesPerSec    uint32
	avgBytesPerSec   uint32
	blockAlign, bits uint16
	extra            uint16
}

// waveHdr — WAVEHDR: живет в памяти VirtualAlloc, а не в куче Go — драйвер
// держит указатель между вызовами.
type waveHdr struct {
	data     uintptr
	length   uint32
	recorded uint32
	user     uintptr
	flags    uint32
	loops    uint32
	next     uintptr
	reserved uintptr
}

var out = bufio.NewWriter(os.Stdout)

func emit(v map[string]any) {
	b, _ := json.Marshal(v)
	out.Write(append(b, '\n')) //nolint:errcheck
	out.Flush()                //nolint:errcheck
}

func fail(msg string, code int) {
	emit(map[string]any{"error": msg})
	os.Exit(code)
}

func arg(name string) string {
	for i, a := range os.Args {
		if a == name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func has(name string) bool { return slices.Contains(os.Args[1:], name) }

func peek(src uintptr, dst unsafe.Pointer, n int) { moveMemory.Call(uintptr(dst), src, uintptr(n)) } //nolint:errcheck
func poke(dst uintptr, src unsafe.Pointer, n int) { moveMemory.Call(dst, uintptr(src), uintptr(n)) } //nolint:errcheck

func main() {
	if has("--check") {
		// Разрешение на микрофон Windows спрашивает сама; своего распознавания нет.
		emit(map[string]any{"auth": "authorized", "onDevice": false, "available": false})
		return
	}
	path := arg("--record")
	if path == "" {
		fail("на Windows диктовка идет через облако или свой сервер: Настройки → Чат и модель → Распознавание", 5)
	}
	pause, _ := strconv.ParseFloat(arg("--pause"), 64)
	record(path, time.Duration(pause*float64(time.Second)))
}

// mic is an open waveIn device with its buffers in VirtualAlloc memory.
type mic struct {
	h     uintptr
	hdrs  []uintptr
	head  int // самый старый буфер в очереди драйвера
	chunk []byte
}

func openMic() *mic {
	wf := waveFormat{tag: 1, channels: 1, samplesPerSec: rate, avgBytesPerSec: rate * 2, blockAlign: 2, bits: 16}
	m := &mic{hdrs: make([]uintptr, numBufs), chunk: make([]byte, bufBytes)}
	// waveInOpen(phwi, uDeviceID, pwfx, dwCallback, dwInstance, fdwOpen): без
	// колбэка — буферы опрашиваются по флагу WHDR_DONE.
	if r, _, _ := waveInOpen.Call(uintptr(unsafe.Pointer(&m.h)), waveMapper, uintptr(unsafe.Pointer(&wf)), 0, 0, 0); r != 0 {
		fail(fmt.Sprintf("микрофон не открылся (код %d): проверьте Параметры → Конфиденциальность → Микрофон → доступ классическим приложениям", r), 4)
	}
	mem, _, _ := virtualAlloc.Call(0, numBufs*(hdrSize+bufBytes), 0x3000, 0x04) // MEM_COMMIT|MEM_RESERVE, PAGE_READWRITE
	if mem == 0 {
		fail("запись не настроилась: нет памяти", 4)
	}
	for i := range m.hdrs {
		m.hdrs[i] = mem + uintptr(i*hdrSize)
		hd := waveHdr{data: mem + numBufs*hdrSize + uintptr(i*bufBytes), length: bufBytes}
		poke(m.hdrs[i], unsafe.Pointer(&hd), hdrSize)
		waveInPrepare.Call(m.h, m.hdrs[i], hdrSize) //nolint:errcheck
		waveInAdd.Call(m.h, m.hdrs[i], hdrSize)     //nolint:errcheck
	}
	if r, _, _ := waveInStart.Call(m.h); r != 0 {
		fail(fmt.Sprintf("микрофон не начал запись (код %d)", r), 4)
	}
	return m
}

// drain hands each filled buffer to take and gives it back to the driver,
// пока запись идет (requeue).
// drain takes the filled buffers in the driver's order: очередь — кольцо от
// самого старого (head), а не номера 0..7, иначе после перестановки куски
// записи шли бы не по порядку (ревью #637).
func (m *mic) drain(requeue bool, take func([]byte)) {
	for range m.hdrs {
		hp := m.hdrs[m.head]
		var hd waveHdr
		peek(hp, unsafe.Pointer(&hd), hdrSize)
		if hd.flags&whdrDone == 0 {
			return
		}
		m.head = (m.head + 1) % len(m.hdrs)
		n := int(hd.recorded)
		peek(hd.data, unsafe.Pointer(&m.chunk[0]), n)
		take(m.chunk[:n])
		hd.flags &^= whdrDone
		hd.recorded = 0
		poke(hp, unsafe.Pointer(&hd), hdrSize)
		if requeue {
			waveInAdd.Call(m.h, hp, hdrSize) //nolint:errcheck
		}
	}
}

// finish stops recording and takes the tail: waveInReset возвращает и
// недописанный буфер — без этого терялся последний слог (ревью #637).
func (m *mic) finish(take func([]byte)) {
	waveInReset.Call(m.h) //nolint:errcheck
	m.drain(false, take)
	m.close()
}

func (m *mic) close() {
	waveInReset.Call(m.h) //nolint:errcheck
	for _, hp := range m.hdrs {
		waveInUnprep.Call(m.h, hp, hdrSize) //nolint:errcheck
	}
	waveInClose.Call(m.h) //nolint:errcheck
}

func record(path string, pause time.Duration) {
	m := openMic()
	emit(map[string]any{"ready": true, "onDevice": false})
	stop := make(chan struct{})
	go func() { // стоп — закрытый stdin, как у помощника Мака
		_, _ = bufio.NewReader(os.Stdin).ReadString(0)
		close(stop)
	}()
	var (
		pcm        bytes.Buffer
		spoke      bool
		began      = time.Now()
		quietSince = began
	)
	take := func(b []byte) {
		pcm.Write(b)
		if level(b) > silenceLevel {
			spoke, quietSince = true, time.Now()
		}
	}
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		case <-time.After(20 * time.Millisecond):
		}
		m.drain(true, take)
		if pause > 0 && (spoke && time.Since(quietSince) > pause || !spoke && time.Since(began) > noSpeechLimit) {
			done = true
		}
	}
	m.finish(take)
	if err := os.WriteFile(path, wav(pcm.Bytes()), 0o600); err != nil {
		fail("файл записи не записался: "+err.Error(), 4)
	}
	emit(map[string]any{"recorded": path, "final": true, "silent": !spoke})
}
