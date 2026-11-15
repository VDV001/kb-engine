package main

// Работник ящика (#622, этап 3). Заготовка без поведения.

import "context"

type mailWorker struct{}

func newMailWorker(*folderSettings, func(ctx context.Context, chat, question string) (string, string, error), string) *mailWorker {
	return &mailWorker{}
}

func (w *mailWorker) tick(context.Context) error { return nil }
