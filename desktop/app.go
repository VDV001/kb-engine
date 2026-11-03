package main

import (
	"context"
	"os/exec"
)

// fileReader is the part of the file index the app needs: the same trust
// boundary the API uses, so a path the index would refuse is not opened either.
type fileReader interface {
	Read(path string) (string, error)
}

// App is bound into the window: its exported methods are callable from the
// page as window.go.main.App.<Method>.
type App struct {
	ctx   context.Context
	files fileReader
	run   func(name string, args ...string) error
}

func newApp(files fileReader) *App {
	return &App{files: files, run: func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	}}
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// Open opens a found file in its default application.
//
// Путь приходит со страницы, то есть снаружи: прежде чем звать open, он
// проходит ту же проверку, что и чтение через API. Иначе окно открыло бы
// любой файл диска, включая .env, по одному вызову из консоли страницы.
func (a *App) Open(path string) error {
	if _, err := a.files.Read(path); err != nil {
		return err
	}
	return a.run("open", path)
}

// Reveal shows a found file in Finder, under the same check as Open.
func (a *App) Reveal(path string) error {
	if _, err := a.files.Read(path); err != nil {
		return err
	}
	return a.run("open", "-R", path)
}
