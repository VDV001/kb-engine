package cdpbrowser

import (
	"context"
	"errors"
)

// Options of a launched Chrome.
type Options struct {
	Path     string // пусто — найти установленный Chrome
	Profile  string // отдельная папка профиля: основной Chrome порт отладки не открывает
	Width    int
	Height   int
	Headless bool // для проверок без окна на экране
}

// Chrome is a running browser; it is a Screen.
type Chrome struct{}

// Launch starts Chrome and attaches to its first page.
func Launch(context.Context, Options) (*Chrome, error) { return nil, errors.New("not implemented") }

// Close stops the browser.
func (*Chrome) Close() error { return nil }

// Next implements Screen.
func (*Chrome) Next(context.Context, uint64) (Frame, error) {
	return Frame{}, errors.New("not implemented")
}

// Input implements Screen.
func (*Chrome) Input(context.Context, Input) error { return errors.New("not implemented") }

var _ Screen = (*Chrome)(nil)
