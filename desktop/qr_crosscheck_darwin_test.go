//go:build darwin && cgo

package main

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/qrcode"
)

// Свой кодировщик QR (internal/adapter/qrcode) рисует QR окна на Windows. Здесь
// его читает детектор Apple (CIDetector) — чужой декодер, проверка не
// закольцована: каждая длина 1–150 и дальше с шагом до 666 байт, все версии
// 1–20 и все восемь масок встречаются.
func TestOwnQREncoderReadByCoreImage(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/?#[]@!$&'()*+,;=%"
	lengths := []int{}
	for n := 1; n <= 150; n++ {
		lengths = append(lengths, n)
	}
	for n := 151; n <= 666; n += 11 {
		lengths = append(lengths, n)
	}
	lengths = append(lengths, 666)
	for _, n := range lengths {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[r.IntN(len(alphabet))]
		}
		text := string(b)
		c, err := qrcode.Encode(b)
		if err != nil {
			t.Fatalf("%d байт: %v", n, err)
		}
		png, err := c.PNG(qrScale)
		if err != nil {
			t.Fatal(err)
		}
		got, err := qrDecode(png)
		if err != nil || got != text {
			t.Fatalf("%d байт (размер %d): CoreImage прочел %q, %v", n, c.Size, got, err)
		}
	}
	// Ссылка привязки как в жизни: адрес, токен и ключ во фрагменте.
	link := fmt.Sprintf("https://kb.example.ru/m/#pair=%s.%s", "tok_"+string(make([]byte, 0)), "k1.AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEfGhIjK")
	c, _ := qrcode.Encode([]byte(link))
	png, _ := c.PNG(qrScale)
	if got, err := qrDecode(png); err != nil || got != link {
		t.Fatalf("ссылка привязки: %q, %v", got, err)
	}
}
