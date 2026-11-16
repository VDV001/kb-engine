package main

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Звук помощника — без системных вызовов: проверяется на любой ОС (wav_test.go).

const (
	rate         = 16000
	silenceLevel = 0.012 // RMS, как у помощника Мака: тише — тишина
)

// level — RMS отсчетов, от 0 до 1.
func level(b []byte) float64 {
	n := len(b) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := range n {
		s := float64(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768
		sum += s * s
	}
	return math.Sqrt(sum / float64(n))
}

// wav wraps 16 кГц моно 16 бит PCM into a RIFF/WAVE file.
func wav(pcm []byte) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + len(pcm)))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(len(pcm)))
	b.Write(pcm)
	return b.Bytes()
}
