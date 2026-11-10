package main

import (
	"context"
	"errors"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
)

// browserScreen is a launched browser as the command uses it.
type browserScreen interface {
	cdpbrowser.Screen
	Close() error
}

func runBrowser(args []string, stdout, stderr io.Writer) int {
	return browserServe(context.Background(), args, stdout, stderr, nil)
}

func browserServe(context.Context, []string, io.Writer, io.Writer, func(context.Context, cdpbrowser.Options) (browserScreen, error)) int {
	return 1
}

func ensureBrowserToken(string) (string, error) { return "", errors.New("not implemented") }
