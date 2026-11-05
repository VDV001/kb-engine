package main

import (
	"context"
	"io"
	"net/http"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

func tenantsHandler(context.Context, blob.Store, io.Writer) http.Handler {
	return http.NotFoundHandler()
}
