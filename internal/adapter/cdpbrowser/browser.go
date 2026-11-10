// Package cdpbrowser is the «Браузер» module's server: a Chrome the person
// installed, driven over the DevTools protocol, shown in the window as a
// picture and clicked back through a few validated events (#BROWSER).
package cdpbrowser

import (
	"context"
	"time"
)

// Размер экрана вкладки: один на сервер и на страницу окна (окно масштабирует картинку).
const (
	DefaultWidth  = 1280
	DefaultHeight = 800
)

// Frame is one picture of the page; Seq grows with every change.
type Frame struct {
	Seq  uint64
	JPEG []byte
	URL  string // адрес главного фрейма: человек вводит пароли и должен видеть, где он
}

// Input is one thing the person did on the picture. Только то, что Handler
// проверил: тип из списка, координаты внутри экрана, адрес http(s).
type Input struct {
	T    string  `json:"t"`    // mouse | wheel | text | key | nav | resize (X, Y — ширина и высота экрана)
	Type string  `json:"type"` // mouse: pressed | released | moved
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	Text string  `json:"text"` // text: вставляемый текст; nav: адрес или back | forward | reload
	Key  string  `json:"key"`
	// Count — какой это щелчок подряд (1-3); Mods — зажатые Alt=1, Ctrl=2, Meta=4, Shift=8 (маска CDP).
	Count int `json:"count"`
	Mods  int `json:"mods"`
}

// Screen is the browser as the handler sees it.
type Screen interface {
	// Next blocks until a frame newer than after exists or ctx ends.
	Next(ctx context.Context, after uint64) (Frame, error)
	Input(ctx context.Context, in Input) error
}

// Config of the handler.
type Config struct {
	Token         string        // обязателен: CDP отдает браузер целиком
	Width, Height int           // размер экрана в пикселях страницы
	Wait          time.Duration // сколько /frame ждет новый кадр до 204
	InputWait     time.Duration // сколько /input ждет браузер; пусто — 10 с
}

const defaultInputWait = 10 * time.Second
