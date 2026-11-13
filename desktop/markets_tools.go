package main

import (
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// shopExtras is not written yet.
func shopExtras(s *folderSettings, base string, hc *http.Client, now func() time.Time) []chat.Extra {
	return nil
}
