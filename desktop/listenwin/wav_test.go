package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// WAV, который уходит серверу распознавания: заголовок RIFF на 44 байта,
// 16 кГц моно 16 бит, длины в заголовке сходятся с данными.
func TestWAV(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0}
	w := wav(pcm)
	if len(w) != 44+len(pcm) || !bytes.HasPrefix(w, []byte("RIFF")) || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" {
		t.Fatalf("заголовок: % x", w[:44])
	}
	le := binary.LittleEndian
	if le.Uint32(w[4:]) != uint32(36+len(pcm)) || le.Uint32(w[24:]) != rate || le.Uint16(w[22:]) != 1 || le.Uint16(w[34:]) != 16 || le.Uint32(w[40:]) != uint32(len(pcm)) {
		t.Fatalf("поля: % x", w[:44])
	}
}

// Порог тишины как у помощника Мака: тихий шум — тишина, речь — нет.
func TestLevel(t *testing.T) {
	sample := func(v int16, n int) []byte {
		b := make([]byte, 2*n)
		for i := range n {
			binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
		}
		return b
	}
	if l := level(sample(100, 160)); l > silenceLevel {
		t.Fatalf("шум 100/32768: уровень %f — выше порога", l)
	}
	if l := level(sample(3000, 160)); l <= silenceLevel {
		t.Fatalf("речь 3000/32768: уровень %f — ниже порога", l)
	}
	if level(nil) != 0 {
		t.Fatal("пусто — не ноль")
	}
}
